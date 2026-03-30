package server

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/robfig/cron/v3"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/config"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/mcp"
	"github.com/nexus-chat/nexus/internal/roles"
	"github.com/nexus-chat/nexus/internal/search"
	"github.com/nexus-chat/nexus/internal/vectorstore"
	"github.com/nexus-chat/nexus/web"
)

type Server struct {
	cfg         *config.Config
	global      *db.Global
	ws          *db.WorkspaceManager
	jwt         *auth.JWTManager
	hubs        *hub.Manager
	mux         *http.ServeMux
	mcpMgrs     sync.Map // map[slug]*mcp.Manager
	cron        *cron.Cron
	search      *search.IndexManager
	asynqClient *asynq.Client
	asynqServer *asynq.Server
	vectors     *vectorstore.VectorStore
	convTracker *ConversationTracker
	agentSem    chan struct{} // semaphore to limit concurrent agent/brain goroutines
	netLog      *NetworkLog
	bridges     sync.Map // map[slug]*BridgeConn — one Ollama bridge per workspace
	bootedAt    time.Time
}

func Run(cfg *config.Config) error {
	logger.Init(cfg.Dev)

	global, err := db.OpenGlobal(cfg.DataDir)
	if err != nil {
		return err
	}
	defer global.Close()

	ws := db.NewWorkspaceManager(cfg.DataDir)
	defer ws.CloseAll()

	// Load or generate JWT secret
	jwtMgr, err := loadOrCreateJWTSecret(global)
	if err != nil {
		return fmt.Errorf("jwt setup: %w", err)
	}

	netLog := NewNetworkLog(500) // keep last 500 outbound requests
	brain.SetGlobalTransport(&LoggingTransport{
		Inner:   http.DefaultTransport,
		Log:     netLog,
		Purpose: "LLM",
	})

	s := &Server{
		cfg:         cfg,
		global:      global,
		ws:          ws,
		jwt:         jwtMgr,
		hubs:        hub.NewManager(),
		mux:         http.NewServeMux(),
		cron:        cron.New(),
		search:      search.NewIndexManager(cfg.DataDir),
		convTracker: NewConversationTracker(),
		agentSem:    make(chan struct{}, 20), // max 20 concurrent agent/brain goroutines
		netLog:      netLog,
		bootedAt:    time.Now(),
	}
	// Initialize asynq task queue (optional — falls back to goroutines)
	if err := s.initAsynq(); err != nil {
		logger.WithCategory(logger.CatSystem).Warn().Err(err).Msg("asynq init failed, falling back to goroutines")
	}

	// Initialize Qdrant vector store (optional — falls back to SQL LIKE)
	if cfg.QdrantURL != "" {
		vs, err := vectorstore.New(cfg.QdrantURL)
		if err != nil {
			logger.WithCategory(logger.CatSystem).Warn().Err(err).Msg("qdrant init failed, falling back to SQL LIKE")
		} else {
			s.vectors = vs
		}
	}

	s.routes()
	s.scheduleHeartbeats()
	s.scheduleReflections()
	s.scheduleCalendarReminders()
	s.cron.Start()
	go s.startSMTPServer()

	// Auto-reindex search for all workspaces
	go s.reindexAllWorkspaces()

	return s.listenAndServe()
}

// loggingMiddleware wraps the mux to log every HTTP request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip noisy endpoints and WebSocket endpoints (statusWriter breaks http.Hijacker)
		if r.URL.Path == "/health" || r.URL.Path == "/metrics" || r.URL.Path == "/ws" || strings.HasSuffix(r.URL.Path, "/bridge") {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		dur := time.Since(start)

		userID := ""
		if claims := auth.GetClaims(r); claims != nil {
			userID = claims.UserID
		}

		logger.WithCategory(logger.CatAPI).Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", sw.status).
			Dur("duration", dur).
			Str("user_id", userID).
			Msg("request")
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func loadOrCreateJWTSecret(global *db.Global) (*auth.JWTManager, error) {
	var secretHex string
	err := global.DB.QueryRow("SELECT secret FROM jwt_secrets WHERE id = 1").Scan(&secretHex)
	if err == nil {
		secret, err := hex.DecodeString(secretHex)
		if err != nil {
			return nil, fmt.Errorf("decoding jwt secret: %w", err)
		}
		return auth.NewJWTManager(secret), nil
	}
	// Generate new secret
	secret, err := auth.GenerateSecret()
	if err != nil {
		return nil, err
	}
	secretHex = hex.EncodeToString(secret)
	_, err = global.DB.Exec("INSERT INTO jwt_secrets (id, secret) VALUES (1, ?)", secretHex)
	if err != nil {
		return nil, fmt.Errorf("storing jwt secret: %w", err)
	}
	return auth.NewJWTManager(secret), nil
}

func (s *Server) routes() {
	// Public routes
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.Handle("GET /metrics", promhttp.Handler())
	s.mux.HandleFunc("POST /api/workspaces", s.handleCreateWorkspace)
	s.mux.HandleFunc("POST /api/workspaces/{slug}/join", s.handleJoinWorkspace)
	s.mux.HandleFunc("POST /api/join", s.handleJoinByCode)
	s.mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("GET /api/auth/config", s.handleAuthConfig)
	s.mux.HandleFunc("POST /api/auth/forgot", s.handleForgotPassword)
	s.mux.HandleFunc("POST /api/auth/reset", s.handleResetPassword)
	s.mux.HandleFunc("POST /api/auth/verify", s.handleVerifyEmail)
	s.mux.HandleFunc("GET /api/briefs/public/{token}", s.handlePublicBrief)

	// Authenticated routes (with permission checks)
	authed := auth.Middleware(s.jwt)
	s.mux.Handle("POST /api/auth/switch-workspace", authed(http.HandlerFunc(s.handleSwitchWorkspace)))
	s.mux.Handle("GET /api/auth/me", authed(http.HandlerFunc(s.handleGetMe)))
	s.mux.Handle("PUT /api/auth/me", authed(http.HandlerFunc(s.handleUpdateMe)))
	s.mux.Handle("PUT /api/auth/me/password", authed(http.HandlerFunc(s.handleChangePassword)))
	s.mux.Handle("GET /api/auth/workspaces", authed(http.HandlerFunc(s.handleListWorkspaces)))
	s.mux.Handle("GET /api/workspaces/{slug}", authed(http.HandlerFunc(s.handleGetWorkspace)))
	s.mux.Handle("GET /api/workspaces/{slug}/info", authed(http.HandlerFunc(s.handleGetWorkspaceInfo)))
	s.mux.Handle("GET /api/workspaces/{slug}/channels", authed(http.HandlerFunc(s.handleListChannels)))
	s.mux.Handle("GET /api/workspaces/{slug}/channels/{channelID}/messages", authed(http.HandlerFunc(s.handleMessageHistory)))
	s.mux.Handle("GET /api/workspaces/{slug}/channels/{channelID}/messages/{messageID}/thread", authed(http.HandlerFunc(s.handleGetThread)))
	s.mux.Handle("PUT /api/workspaces/{slug}/channels/{channelID}/favorite", authed(http.HandlerFunc(s.handleToggleFavorite)))
	s.mux.Handle("GET /api/workspaces/{slug}/online", authed(http.HandlerFunc(s.handleOnlineMembers)))
	s.mux.Handle("GET /api/workspaces/{slug}/search", authed(http.HandlerFunc(s.handleSearch)))

	// Permission-gated routes
	s.mux.Handle("POST /api/workspaces/{slug}/invite", authed(http.HandlerFunc(s.requirePerm(roles.PermWorkspaceInvite, s.handleCreateInvite))))
	s.mux.Handle("POST /api/workspaces/{slug}/invite/email", authed(http.HandlerFunc(s.requirePerm(roles.PermWorkspaceInvite, s.handleInviteByEmail))))
	s.mux.Handle("POST /api/workspaces/{slug}/channels", authed(http.HandlerFunc(s.requirePerm(roles.PermChannelCreate, s.handleCreateChannel))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/channels/{channelID}", authed(http.HandlerFunc(s.handleDeleteChannel)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/channels/{channelID}/members/{memberID}", authed(http.HandlerFunc(s.requireAdmin(s.handleKickChannelMember))))
	s.mux.Handle("POST /api/workspaces/{slug}/channels/{channelID}/join", authed(http.HandlerFunc(s.handleJoinChannel)))
	s.mux.Handle("POST /api/workspaces/{slug}/channels/{channelID}/leave", authed(http.HandlerFunc(s.handleLeaveChannel)))
	s.mux.Handle("POST /api/workspaces/{slug}/channels/{channelID}/invite", authed(http.HandlerFunc(s.handleInviteToChannel)))
	s.mux.Handle("GET /api/workspaces/{slug}/channels/{channelID}/members", authed(http.HandlerFunc(s.handleListChannelMembers)))
	s.mux.Handle("POST /api/workspaces/{slug}/channels/{channelID}/pin", authed(http.HandlerFunc(s.handlePinMessage)))
	s.mux.Handle("POST /api/workspaces/{slug}/channels/{channelID}/unpin", authed(http.HandlerFunc(s.handleUnpinMessage)))
	s.mux.Handle("GET /api/workspaces/{slug}/channels/{channelID}/pins", authed(http.HandlerFunc(s.handleListPinnedMessages)))
	s.mux.Handle("GET /api/workspaces/{slug}/channels/{channelID}/memory-pins", authed(http.HandlerFunc(s.handlePinnedMemoryMessageIDs)))

	// Admin-only: role & member management
	s.mux.Handle("GET /api/roles", authed(http.HandlerFunc(s.handleListRoles)))
	s.mux.Handle("GET /api/workspaces/{slug}/members/{memberID}", authed(http.HandlerFunc(s.handleGetMember)))
	s.mux.Handle("PUT /api/workspaces/{slug}/members/role", authed(http.HandlerFunc(s.requireAdmin(s.handleUpdateRole))))
	s.mux.Handle("PUT /api/workspaces/{slug}/members/permission", authed(http.HandlerFunc(s.requireAdmin(s.handleUpdatePermission))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/members/{memberID}", authed(http.HandlerFunc(s.requireAdmin(s.handleKickMember))))

	// Tasks
	s.mux.Handle("POST /api/workspaces/{slug}/tasks", authed(http.HandlerFunc(s.requirePerm(roles.PermTaskCreate, s.handleCreateTask))))
	s.mux.Handle("GET /api/workspaces/{slug}/tasks", authed(http.HandlerFunc(s.handleListTasks)))
	s.mux.Handle("GET /api/workspaces/{slug}/tasks/{taskID}", authed(http.HandlerFunc(s.handleGetTask)))
	s.mux.Handle("PUT /api/workspaces/{slug}/tasks/{taskID}", authed(http.HandlerFunc(s.requirePerm(roles.PermTaskEdit, s.handleUpdateTask))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/tasks/{taskID}", authed(http.HandlerFunc(s.requirePerm(roles.PermTaskDelete, s.handleDeleteTask))))
	s.mux.Handle("GET /api/workspaces/{slug}/tasks/{taskID}/runs", authed(http.HandlerFunc(s.handleListTaskRuns)))

	// Documents
	s.mux.Handle("POST /api/workspaces/{slug}/documents", authed(http.HandlerFunc(s.requirePerm(roles.PermDocCreate, s.handleCreateDoc))))
	s.mux.Handle("GET /api/workspaces/{slug}/documents", authed(http.HandlerFunc(s.handleListDocs)))
	s.mux.Handle("GET /api/workspaces/{slug}/documents/{docID}", authed(http.HandlerFunc(s.handleGetDoc)))
	s.mux.Handle("PUT /api/workspaces/{slug}/documents/{docID}", authed(http.HandlerFunc(s.requirePerm(roles.PermDocEdit, s.handleUpdateDoc))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/documents/{docID}", authed(http.HandlerFunc(s.requirePerm(roles.PermDocDelete, s.handleDeleteDoc))))

	// Files
	s.mux.Handle("POST /api/workspaces/{slug}/channels/{channelID}/files", authed(http.HandlerFunc(s.requirePerm(roles.PermFileUpload, s.handleUploadFile))))
	s.mux.Handle("GET /api/workspaces/{slug}/files", authed(http.HandlerFunc(s.handleListFiles)))
	s.mux.HandleFunc("GET /api/workspaces/{slug}/files/{hash}", s.handleDownloadFile)

	// Folders & file management
	s.mux.Handle("POST /api/workspaces/{slug}/folders", authed(http.HandlerFunc(s.requirePerm(roles.PermFileUpload, s.handleCreateFolder))))
	s.mux.Handle("GET /api/workspaces/{slug}/folders", authed(http.HandlerFunc(s.handleListFolders)))
	s.mux.Handle("PUT /api/workspaces/{slug}/folders/{folderID}", authed(http.HandlerFunc(s.requirePerm(roles.PermFileUpload, s.handleUpdateFolder))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/folders/{folderID}", authed(http.HandlerFunc(s.requirePerm(roles.PermFileUpload, s.handleDeleteFolder))))
	s.mux.Handle("POST /api/workspaces/{slug}/folders/{folderID}/files", authed(http.HandlerFunc(s.requirePerm(roles.PermFileUpload, s.handleUploadToFolder))))
	s.mux.Handle("PUT /api/workspaces/{slug}/files/{fileID}/update", authed(http.HandlerFunc(s.requirePerm(roles.PermFileUpload, s.handleUpdateFile))))
	s.mux.Handle("PUT /api/workspaces/{slug}/files/{fileID}/move", authed(http.HandlerFunc(s.requirePerm(roles.PermFileUpload, s.handleMoveFile))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/files/{fileID}/delete", authed(http.HandlerFunc(s.requirePerm(roles.PermFileUpload, s.handleDeleteFile))))
	s.mux.Handle("POST /api/workspaces/{slug}/files/{fileID}/duplicate", authed(http.HandlerFunc(s.requirePerm(roles.PermFileUpload, s.handleDuplicateFile))))

	// Brain
	s.mux.Handle("GET /api/workspaces/{slug}/brain/prompt", authed(http.HandlerFunc(s.handleGetBrainPrompt)))
	s.mux.Handle("POST /api/workspaces/{slug}/channels/{channelId}/brain-message", authed(http.HandlerFunc(s.requirePerm(roles.PermChatSend, s.handleSaveBrainMessage))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/execute-tool", authed(http.HandlerFunc(s.requirePerm(roles.PermBrainDM, s.handleExecuteTool))))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/tools", authed(http.HandlerFunc(s.requirePerm(roles.PermBrainDM, s.handleGetBrainTools))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/webllm-context", authed(http.HandlerFunc(s.requirePerm(roles.PermBrainDM, s.handleWebLLMContext))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/welcome", authed(http.HandlerFunc(s.requireAdmin(s.handleBrainWelcome))))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/settings", authed(http.HandlerFunc(s.handleGetBrainSettings)))
	s.mux.Handle("PUT /api/workspaces/{slug}/brain/settings", authed(http.HandlerFunc(s.handleUpdateBrainSettings)))

	// Ollama Bridge (WebSocket — auth via query param like /ws, no middleware wrapping)
	s.mux.HandleFunc("GET /api/workspaces/{slug}/bridge", s.handleBridge)
	s.mux.Handle("GET /api/workspaces/{slug}/bridge/status", authed(http.HandlerFunc(s.handleBridgeStatus)))
	s.mux.Handle("GET /api/workspaces/{slug}/ollama/models", authed(http.HandlerFunc(s.requireAdmin(s.handleOllamaModels))))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/definitions/{file}", authed(http.HandlerFunc(s.handleGetBrainDefinition)))
	s.mux.Handle("PUT /api/workspaces/{slug}/brain/definitions/{file}", authed(http.HandlerFunc(s.handleUpdateBrainDefinition)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/memories", authed(http.HandlerFunc(s.handleListMemories)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/memories/{memoryID}", authed(http.HandlerFunc(s.handleDeleteMemory)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/memories", authed(http.HandlerFunc(s.handleClearMemories)))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/memories/pin", authed(http.HandlerFunc(s.handlePinMemory)))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/memories/extract", authed(http.HandlerFunc(s.requireAdmin(s.handleExtractNow))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/reflect", authed(http.HandlerFunc(s.requireAdmin(s.handleReflectNow))))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/reflections", authed(http.HandlerFunc(s.requireAdmin(s.handleReflectionHistory))))
	s.mux.Handle("GET /api/workspaces/{slug}/usage", authed(http.HandlerFunc(s.requireAdmin(s.handleGetUsage))))

	// Living Briefs
	s.mux.Handle("GET /api/workspaces/{slug}/briefs", authed(http.HandlerFunc(s.handleListBriefs)))
	s.mux.Handle("GET /api/workspaces/{slug}/briefs/templates", authed(http.HandlerFunc(s.handleListBriefTemplates)))
	s.mux.Handle("GET /api/workspaces/{slug}/briefs/{briefID}", authed(http.HandlerFunc(s.handleGetBrief)))
	s.mux.Handle("POST /api/workspaces/{slug}/briefs", authed(http.HandlerFunc(s.requireAdmin(s.handleCreateBrief))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/briefs/{briefID}", authed(http.HandlerFunc(s.requireAdmin(s.handleDeleteBrief))))
	s.mux.Handle("POST /api/workspaces/{slug}/briefs/{briefID}/generate", authed(http.HandlerFunc(s.requireAdmin(s.handleGenerateBrief))))
	s.mux.Handle("POST /api/workspaces/{slug}/briefs/{briefID}/share", authed(http.HandlerFunc(s.requireAdmin(s.handleShareBrief))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/briefs/{briefID}/share", authed(http.HandlerFunc(s.requireAdmin(s.handleUnshareBrief))))

	// Portable workspace
	s.mux.Handle("GET /api/workspaces/{slug}/export", authed(http.HandlerFunc(s.requireAdmin(s.handleExportWorkspace))))
	s.mux.Handle("POST /api/workspaces/import", authed(http.HandlerFunc(s.handleImportWorkspace)))

	// Workspace destroy (kill switch)
	s.mux.Handle("DELETE /api/workspaces/{slug}/destroy", authed(http.HandlerFunc(s.requireAdmin(s.handleDestroyWorkspace))))

	// Network transparency
	s.mux.Handle("GET /api/workspaces/{slug}/network-log", authed(http.HandlerFunc(s.requireAdmin(s.handleNetworkLog))))

	s.mux.Handle("GET /api/workspaces/{slug}/brain/actions", authed(http.HandlerFunc(s.handleListActions)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/skills", authed(http.HandlerFunc(s.handleListSkills)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/skills/templates", authed(http.HandlerFunc(s.handleListSkillTemplates)))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/skills/generate", authed(http.HandlerFunc(s.requirePerm(roles.PermSkillManage, s.handleGenerateSkill))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/skills", authed(http.HandlerFunc(s.requirePerm(roles.PermSkillManage, s.handleCreateSkill))))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/skills/{file}", authed(http.HandlerFunc(s.handleGetSkill)))
	s.mux.Handle("PUT /api/workspaces/{slug}/brain/skills/{file}", authed(http.HandlerFunc(s.requirePerm(roles.PermSkillManage, s.handleUpdateSkill))))
	s.mux.Handle("PUT /api/workspaces/{slug}/brain/skills/{file}/toggle", authed(http.HandlerFunc(s.requirePerm(roles.PermSkillManage, s.handleToggleSkill))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/skills/{file}", authed(http.HandlerFunc(s.requirePerm(roles.PermSkillManage, s.handleDeleteSkill))))

	// Brain Knowledge
	s.mux.Handle("POST /api/workspaces/{slug}/brain/knowledge", authed(http.HandlerFunc(s.requirePerm(roles.PermKnowledgeManage, s.handleCreateKnowledge))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/knowledge/upload", authed(http.HandlerFunc(s.requirePerm(roles.PermKnowledgeManage, s.handleUploadKnowledge))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/knowledge/import-url", authed(http.HandlerFunc(s.requirePerm(roles.PermKnowledgeManage, s.handleImportKnowledgeURL))))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/knowledge", authed(http.HandlerFunc(s.handleListKnowledge)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/knowledge/{id}", authed(http.HandlerFunc(s.handleGetKnowledge)))
	s.mux.Handle("PUT /api/workspaces/{slug}/brain/knowledge/{id}", authed(http.HandlerFunc(s.requirePerm(roles.PermKnowledgeManage, s.handleUpdateKnowledge))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/knowledge/{id}", authed(http.HandlerFunc(s.requirePerm(roles.PermKnowledgeManage, s.handleDeleteKnowledge))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/reindex", authed(http.HandlerFunc(s.requireAdmin(s.handleReindexEmbeddings))))

	// Agents
	s.mux.Handle("POST /api/workspaces/{slug}/agents", authed(http.HandlerFunc(s.requirePerm(roles.PermAgentCreate, s.handleCreateAgent))))
	s.mux.Handle("GET /api/workspaces/{slug}/agents", authed(http.HandlerFunc(s.handleListAgents)))
	s.mux.Handle("GET /api/workspaces/{slug}/agents/templates", authed(http.HandlerFunc(s.handleListAgentTemplates)))
	s.mux.Handle("GET /api/workspaces/{slug}/agents/{agentID}", authed(http.HandlerFunc(s.handleGetAgent)))
	s.mux.Handle("PUT /api/workspaces/{slug}/agents/{agentID}", authed(http.HandlerFunc(s.requirePerm(roles.PermAgentManage, s.handleUpdateAgent))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/agents/{agentID}", authed(http.HandlerFunc(s.requirePerm(roles.PermAgentManage, s.handleDeleteAgent))))
	s.mux.Handle("POST /api/workspaces/{slug}/agents/generate", authed(http.HandlerFunc(s.requirePerm(roles.PermAgentCreate, s.handleGenerateAgentConfig))))
	s.mux.Handle("POST /api/workspaces/{slug}/agents/edit-with-ai", authed(http.HandlerFunc(s.requirePerm(roles.PermAgentManage, s.handleEditAgentWithAI))))
	s.mux.Handle("POST /api/workspaces/{slug}/agents/from-template", authed(http.HandlerFunc(s.requirePerm(roles.PermAgentCreate, s.handleCreateAgentFromTemplate))))

	// Org Chart
	s.mux.Handle("GET /api/workspaces/{slug}/org-chart", authed(http.HandlerFunc(s.handleGetOrgChart)))
	s.mux.Handle("PUT /api/workspaces/{slug}/org-chart/position", authed(http.HandlerFunc(s.requireAdmin(s.handleUpdateOrgPosition))))
	s.mux.Handle("PUT /api/workspaces/{slug}/members/{memberID}/profile", authed(http.HandlerFunc(s.handleUpdateMemberProfile)))

	// Org Roles
	s.mux.Handle("GET /api/workspaces/{slug}/org-chart/roles", authed(http.HandlerFunc(s.handleListOrgRoles)))
	s.mux.Handle("POST /api/workspaces/{slug}/org-chart/roles", authed(http.HandlerFunc(s.requireAdmin(s.handleCreateOrgRole))))
	s.mux.Handle("PUT /api/workspaces/{slug}/org-chart/roles/{roleID}", authed(http.HandlerFunc(s.requireAdmin(s.handleUpdateOrgRole))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/org-chart/roles/{roleID}", authed(http.HandlerFunc(s.requireAdmin(s.handleDeleteOrgRole))))
	s.mux.Handle("PUT /api/workspaces/{slug}/org-chart/roles/{roleID}/fill", authed(http.HandlerFunc(s.requireAdmin(s.handleFillOrgRole))))

	// Agent Skills
	s.mux.Handle("GET /api/workspaces/{slug}/agents/{agentID}/skills", authed(http.HandlerFunc(s.handleListAgentSkills)))
	s.mux.Handle("GET /api/workspaces/{slug}/agents/{agentID}/skills/{file}", authed(http.HandlerFunc(s.handleGetAgentSkill)))
	s.mux.Handle("PUT /api/workspaces/{slug}/agents/{agentID}/skills/{file}", authed(http.HandlerFunc(s.requireAdmin(s.handleUpdateAgentSkill))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/agents/{agentID}/skills/{file}", authed(http.HandlerFunc(s.requireAdmin(s.handleDeleteAgentSkill))))

	// Models
	s.mux.Handle("GET /api/models/browse", authed(http.HandlerFunc(s.handleBrowseModels)))
	s.mux.Handle("GET /api/models", authed(http.HandlerFunc(s.handleGetPinnedModels)))
	s.mux.Handle("GET /api/models/free", authed(http.HandlerFunc(s.handleGetFreeModels)))

	// Workspace models
	s.mux.Handle("GET /api/workspaces/{slug}/models", authed(http.HandlerFunc(s.handleGetWorkspaceModels)))
	s.mux.Handle("POST /api/workspaces/{slug}/models", authed(http.HandlerFunc(s.handleAddWorkspaceModel)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/models/{modelID}", authed(http.HandlerFunc(s.requireAdmin(s.handleRemoveWorkspaceModel))))
	s.mux.Handle("GET /api/workspaces/{slug}/models/check", authed(http.HandlerFunc(s.handleCheckModelAvailability)))
	s.mux.Handle("GET /api/workspaces/{slug}/models/free", authed(http.HandlerFunc(s.handleGetWorkspaceFreeModels)))
	s.mux.Handle("PUT /api/workspaces/{slug}/models/free", authed(http.HandlerFunc(s.requireAdmin(s.handleSetWorkspaceFreeModels))))

	// Waitlist (public)
	s.mux.HandleFunc("POST /api/waitlist", s.handleWaitlist)

	// Announcements (public)
	s.mux.HandleFunc("GET /api/announcements", s.handleGetAnnouncement)

	// Platform Superadmin
	s.mux.Handle("GET /api/admin/stats", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminStats))))
	s.mux.Handle("GET /api/admin/workspaces", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminListWorkspaces))))
	s.mux.Handle("GET /api/admin/accounts", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminListAccounts))))
	s.mux.Handle("PUT /api/admin/workspaces/{slug}/suspend", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminSuspendWorkspace))))
	s.mux.Handle("DELETE /api/admin/workspaces/{slug}", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminDeleteWorkspace))))
	s.mux.Handle("PUT /api/admin/accounts/{accountID}/ban", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminBanAccount))))
	s.mux.Handle("POST /api/admin/impersonate", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminImpersonate))))
	s.mux.Handle("POST /api/admin/workspaces/{slug}/enter", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminEnterWorkspace))))
	s.mux.Handle("GET /api/admin/audit", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminAuditLog))))
	s.mux.Handle("GET /api/admin/workspaces/{slug}/detail", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminWorkspaceDetail))))
	s.mux.Handle("GET /api/admin/workspaces/{slug}/export", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminExportWorkspace))))
	s.mux.Handle("PUT /api/admin/accounts/{accountID}/password", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminResetPassword))))
	s.mux.Handle("POST /api/admin/announcements", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminSetAnnouncement))))
	s.mux.Handle("DELETE /api/admin/announcements", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminClearAnnouncement))))
	s.mux.Handle("GET /api/admin/models", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminGetModels))))
	s.mux.Handle("PUT /api/admin/models", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminSetModels))))
	s.mux.Handle("PUT /api/admin/models/free", authed(http.HandlerFunc(s.requireSuperadmin(s.handleAdminSetFreeModels))))
	s.mux.Handle("GET /api/admin/waitlist", authed(http.HandlerFunc(s.requireSuperadmin(s.handleListWaitlist))))

	// Webhooks (public ingestion endpoint)
	s.mux.HandleFunc("POST /w/{slug}/hook/{token}", s.handleIncomingWebhook)

	// Webhook management (admin)
	s.mux.Handle("POST /api/workspaces/{slug}/brain/webhooks", authed(http.HandlerFunc(s.requireAdmin(s.handleCreateWebhook))))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/webhooks", authed(http.HandlerFunc(s.requireAdmin(s.handleListWebhooks))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/webhooks/{id}", authed(http.HandlerFunc(s.requireAdmin(s.handleDeleteWebhook))))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/webhooks/{id}/events", authed(http.HandlerFunc(s.requireAdmin(s.handleListWebhookEvents))))

	// Email (admin)
	s.mux.Handle("GET /api/workspaces/{slug}/brain/email/threads", authed(http.HandlerFunc(s.requireAdmin(s.handleListEmailThreads))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/email/threads/{id}", authed(http.HandlerFunc(s.requireAdmin(s.handleDeleteEmailThread))))

	// Telegram (public webhook + admin management)
	s.mux.HandleFunc("POST /api/telegram/{slug}/update", s.handleTelegramUpdate)
	s.mux.Handle("GET /api/workspaces/{slug}/brain/telegram/chats", authed(http.HandlerFunc(s.requireAdmin(s.handleListTelegramChats))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/telegram/chats/{id}", authed(http.HandlerFunc(s.requireAdmin(s.handleDeleteTelegramChat))))

	// MCP Templates (public catalog)
	s.mux.HandleFunc("GET /api/mcp-templates", s.handleListMCPTemplates)

	// MCP Servers (admin)
	s.mux.Handle("GET /api/workspaces/{slug}/mcp-servers", authed(http.HandlerFunc(s.requireAdmin(s.handleListMCPServers))))
	s.mux.Handle("POST /api/workspaces/{slug}/mcp-servers", authed(http.HandlerFunc(s.requireAdmin(s.handleCreateMCPServer))))
	s.mux.Handle("GET /api/workspaces/{slug}/mcp-servers/{id}", authed(http.HandlerFunc(s.requireAdmin(s.handleGetMCPServer))))
	s.mux.Handle("PUT /api/workspaces/{slug}/mcp-servers/{id}", authed(http.HandlerFunc(s.requireAdmin(s.handleUpdateMCPServer))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/mcp-servers/{id}", authed(http.HandlerFunc(s.requireAdmin(s.handleDeleteMCPServer))))
	s.mux.Handle("POST /api/workspaces/{slug}/mcp-servers/{id}/refresh", authed(http.HandlerFunc(s.requireAdmin(s.handleRefreshMCPServer))))

	// Calendar
	s.mux.Handle("POST /api/workspaces/{slug}/calendar/events", authed(http.HandlerFunc(s.requirePerm(roles.PermEventCreate, s.handleCreateEvent))))
	s.mux.Handle("GET /api/workspaces/{slug}/calendar/events", authed(http.HandlerFunc(s.handleListEvents)))
	s.mux.Handle("GET /api/workspaces/{slug}/calendar/events/{eventID}", authed(http.HandlerFunc(s.handleGetEvent)))
	s.mux.Handle("PUT /api/workspaces/{slug}/calendar/events/{eventID}", authed(http.HandlerFunc(s.requirePerm(roles.PermEventEdit, s.handleUpdateEvent))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/calendar/events/{eventID}", authed(http.HandlerFunc(s.requirePerm(roles.PermEventDelete, s.handleDeleteEvent))))
	s.mux.Handle("GET /api/workspaces/{slug}/calendar/events/{eventID}/outcome", authed(http.HandlerFunc(s.handleEventOutcome)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/calendar/events/clear-past-agent", authed(http.HandlerFunc(s.handleClearPastAgentEvents)))

	// Activity
	s.mux.Handle("GET /api/workspaces/{slug}/activity", authed(http.HandlerFunc(s.handleListActivity)))
	s.mux.Handle("GET /api/workspaces/{slug}/activity/stats", authed(http.HandlerFunc(s.handleActivityStats)))

	// Social Pulse
	s.mux.Handle("POST /api/workspaces/{slug}/social-pulse", authed(http.HandlerFunc(s.handleCreateSocialPulse)))
	s.mux.Handle("GET /api/workspaces/{slug}/social-pulse", authed(http.HandlerFunc(s.handleListSocialPulses)))
	s.mux.Handle("GET /api/workspaces/{slug}/social-pulse/{pulseID}", authed(http.HandlerFunc(s.handleGetSocialPulse)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/social-pulse/{pulseID}", authed(http.HandlerFunc(s.handleDeleteSocialPulse)))

	// Logs
	s.mux.Handle("GET /api/workspaces/{slug}/logs", authed(http.HandlerFunc(s.handleGetLogs)))

	// WebSocket (auth via query param)
	s.mux.HandleFunc("GET /ws", s.handleWebSocket)

	// Static files with SPA fallback
	s.mux.Handle("GET /", spaHandler(web.Static()))
}

// spaHandler serves static files, falling back to index.html for SPA routes.
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Cache headers: immutable hashed assets get long cache, HTML gets no-cache
		if strings.HasPrefix(path, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else if path == "/" || strings.HasSuffix(path, ".html") {
			w.Header().Set("Cache-Control", "no-cache")
		}

		if path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
			r.URL.Path = "/landing.html"
			fileServer.ServeHTTP(w, r)
			return
		}
		// Serve brief.html for /brief/{token} paths
		if strings.HasPrefix(path, "/brief/") {
			w.Header().Set("Cache-Control", "no-cache")
			r.URL.Path = "/brief.html"
			fileServer.ServeHTTP(w, r)
			return
		}
		// Try to serve the file directly
		clean := path[1:] // strip leading /
		f, err := fsys.Open(clean)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Try .html suffix (pre-rendered pages like /landing → landing.html)
		if f, err := fsys.Open(clean + ".html"); err == nil {
			f.Close()
			r.URL.Path = path + ".html"
			w.Header().Set("Cache-Control", "no-cache")
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fall back to index.html for SPA routing
		w.Header().Set("Cache-Control", "no-cache")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// getMCPManager returns the MCP manager for a workspace, creating and initializing it if needed.
func (s *Server) getMCPManager(slug string) *mcp.Manager {
	if v, ok := s.mcpMgrs.Load(slug); ok {
		return v.(*mcp.Manager)
	}

	mgr := mcp.NewManager()
	actual, loaded := s.mcpMgrs.LoadOrStore(slug, mgr)
	if loaded {
		return actual.(*mcp.Manager)
	}

	// Initialize: connect all enabled servers from DB
	go s.initMCPServers(slug, mgr)
	return mgr
}

// initMCPServers loads and connects all enabled MCP servers for a workspace.
func (s *Server) initMCPServers(slug string, mgr *mcp.Manager) {
	s.fixupMCPCommands(slug)

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	rows, err := wdb.DB.Query(`
		SELECT id, name, transport, command, args, url, env, headers, tool_prefix
		FROM mcp_servers WHERE enabled = 1`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var sid, name, transport, command, argsStr, url, envStr, headersStr, prefix string
		if err := rows.Scan(&sid, &name, &transport, &command, &argsStr, &url, &envStr, &headersStr, &prefix); err != nil {
			continue
		}

		var args []string
		json.Unmarshal([]byte(argsStr), &args)
		var env map[string]string
		json.Unmarshal([]byte(envStr), &env)
		var headers map[string]string
		json.Unmarshal([]byte(headersStr), &headers)

		cfg := mcp.ServerConfig{
			ID:        sid,
			Name:      name,
			Transport: transport,
			Command:   command,
			Args:      args,
			URL:       url,
			Env:       env,
			Headers:   headers,
			Prefix:    prefix,
			Enabled:   true,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		if err := mgr.Connect(ctx, cfg); err != nil {
			logger.WithCategory(logger.CatSystem).Warn().Err(err).Str("workspace", slug).Str("mcp_server", name).Msg("MCP init connect failed")
		}
		cancel()
	}
}

// getAllTools returns built-in tools + all MCP tools for a workspace.
func (s *Server) getAllTools(slug string) []brain.ToolDef {
	mgr := s.getMCPManager(slug)
	mcpTools := mgr.AllTools()

	// When Grok/xAI is active, skip DuckDuckGo tools (Grok has native web/X search)
	xaiOn := s.getBrainSetting(slug, "xai_enabled") == "true"

	if len(mcpTools) == 0 {
		return brain.Tools
	}

	all := make([]brain.ToolDef, len(brain.Tools), len(brain.Tools)+len(mcpTools))
	copy(all, brain.Tools)

	// Deduplicate MCP tools by function name (multiple servers may expose the same tool)
	seen := make(map[string]bool, len(all))
	for _, t := range all {
		seen[t.Function.Name] = true
	}
	for _, t := range mcp.ToToolDefs(mcpTools) {
		if seen[t.Function.Name] {
			continue
		}
		if xaiOn && strings.HasPrefix(t.Function.Name, "ddg__") {
			continue
		}
		seen[t.Function.Name] = true
		all = append(all, t)
	}
	return all
}

// reindexAllWorkspaces triggers a background reindex for each workspace that needs it.
func (s *Server) reindexAllWorkspaces() {
	rows, err := s.global.DB.Query("SELECT slug FROM workspaces")
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var slug string
		rows.Scan(&slug)
		if s.search.NeedsReindex(slug) {
			wdb, err := s.ws.Open(slug)
			if err != nil {
				continue
			}
			s.search.Reindex(slug, wdb.DB)
		}
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

func (s *Server) listenAndServe() error {
	srv := &http.Server{
		Handler:     loggingMiddleware(s.mux),
		IdleTimeout: 120 * time.Second,
		// No ReadTimeout/WriteTimeout — they kill WebSocket connections
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	// On Fly.io, TLS is terminated by the proxy — always use plain HTTP
	flyManaged := os.Getenv("FLY_APP_NAME") != ""

	if s.cfg.Dev || s.cfg.Domain == "" || flyManaged {
		// Plain HTTP (dev, no domain, or Fly.io)
		srv.Addr = s.cfg.Listen
		if flyManaged {
			logger.WithCategory(logger.CatSystem).Info().Str("addr", s.cfg.Listen).Str("domain", s.cfg.Domain).Msg("nexus server listening (Fly.io)")
		} else {
			logger.WithCategory(logger.CatSystem).Info().Str("addr", s.cfg.Listen).Msg("nexus dev server listening")
		}
		go func() { errCh <- srv.ListenAndServe() }()
	} else {
		// Production mode: auto-TLS
		cacheDir := fmt.Sprintf("%s/certs", s.cfg.DataDir)
		if err := os.MkdirAll(cacheDir, 0700); err != nil {
			return fmt.Errorf("creating cert cache dir: %w", err)
		}

		m := &autocert.Manager{
			Cache:      autocert.DirCache(cacheDir),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(s.cfg.Domain),
		}

		srv.Addr = ":443"
		srv.TLSConfig = &tls.Config{GetCertificate: m.GetCertificate}
		logger.WithCategory(logger.CatSystem).Info().Str("domain", s.cfg.Domain).Msg("nexus server listening on :443")

		// HTTP→HTTPS redirect
		go http.ListenAndServe(":80", m.HTTPHandler(nil))
		go func() { errCh <- srv.ListenAndServeTLS("", "") }()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.WithCategory(logger.CatSystem).Info().Msg("shutting down...")
		s.cron.Stop()
		s.search.CloseAll()
		if s.asynqClient != nil {
			s.asynqClient.Close()
		}
		if s.asynqServer != nil {
			s.asynqServer.Shutdown()
		}
		if s.vectors != nil {
			s.vectors.Close()
		}
		if s.convTracker != nil {
			s.convTracker.Stop()
		}
		// Close all MCP managers
		s.mcpMgrs.Range(func(key, value any) bool {
			value.(*mcp.Manager).CloseAll()
			return true
		})
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}
