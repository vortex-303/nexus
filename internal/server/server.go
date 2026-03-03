package server

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/config"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/roles"
	"github.com/nexus-chat/nexus/web"
)

type Server struct {
	cfg    *config.Config
	global *db.Global
	ws     *db.WorkspaceManager
	jwt    *auth.JWTManager
	hubs   *hub.Manager
	mux    *http.ServeMux
}

func Run(cfg *config.Config) error {
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

	s := &Server{
		cfg:    cfg,
		global: global,
		ws:     ws,
		jwt:    jwtMgr,
		hubs:   hub.NewManager(),
		mux:    http.NewServeMux(),
	}
	s.routes()
	s.startHeartbeatRunner()
	go s.startSMTPServer()

	return s.listenAndServe()
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
	s.mux.HandleFunc("POST /api/workspaces", s.handleCreateWorkspace)
	s.mux.HandleFunc("POST /api/workspaces/{slug}/join", s.handleJoinWorkspace)
	s.mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)

	// Authenticated routes (with permission checks)
	authed := auth.Middleware(s.jwt)
	s.mux.Handle("GET /api/auth/me", authed(http.HandlerFunc(s.handleGetMe)))
	s.mux.Handle("PUT /api/auth/me", authed(http.HandlerFunc(s.handleUpdateMe)))
	s.mux.Handle("PUT /api/auth/me/password", authed(http.HandlerFunc(s.handleChangePassword)))
	s.mux.Handle("GET /api/auth/workspaces", authed(http.HandlerFunc(s.handleListWorkspaces)))
	s.mux.Handle("GET /api/workspaces/{slug}", authed(http.HandlerFunc(s.handleGetWorkspace)))
	s.mux.Handle("GET /api/workspaces/{slug}/channels", authed(http.HandlerFunc(s.handleListChannels)))
	s.mux.Handle("GET /api/workspaces/{slug}/channels/{channelID}/messages", authed(http.HandlerFunc(s.handleMessageHistory)))
	s.mux.Handle("GET /api/workspaces/{slug}/online", authed(http.HandlerFunc(s.handleOnlineMembers)))

	// Permission-gated routes
	s.mux.Handle("POST /api/workspaces/{slug}/invite", authed(http.HandlerFunc(s.requirePerm(roles.PermWorkspaceInvite, s.handleCreateInvite))))
	s.mux.Handle("POST /api/workspaces/{slug}/channels", authed(http.HandlerFunc(s.requirePerm(roles.PermChannelCreate, s.handleCreateChannel))))

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

	// Documents
	s.mux.Handle("POST /api/workspaces/{slug}/documents", authed(http.HandlerFunc(s.handleCreateDoc)))
	s.mux.Handle("GET /api/workspaces/{slug}/documents", authed(http.HandlerFunc(s.handleListDocs)))
	s.mux.Handle("GET /api/workspaces/{slug}/documents/{docID}", authed(http.HandlerFunc(s.handleGetDoc)))
	s.mux.Handle("PUT /api/workspaces/{slug}/documents/{docID}", authed(http.HandlerFunc(s.handleUpdateDoc)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/documents/{docID}", authed(http.HandlerFunc(s.handleDeleteDoc)))

	// Files
	s.mux.Handle("POST /api/workspaces/{slug}/channels/{channelID}/files", authed(http.HandlerFunc(s.handleUploadFile)))
	s.mux.Handle("GET /api/workspaces/{slug}/files", authed(http.HandlerFunc(s.handleListFiles)))
	s.mux.HandleFunc("GET /api/workspaces/{slug}/files/{hash}", s.handleDownloadFile)

	// Brain
	s.mux.Handle("GET /api/workspaces/{slug}/brain/settings", authed(http.HandlerFunc(s.handleGetBrainSettings)))
	s.mux.Handle("PUT /api/workspaces/{slug}/brain/settings", authed(http.HandlerFunc(s.handleUpdateBrainSettings)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/definitions/{file}", authed(http.HandlerFunc(s.handleGetBrainDefinition)))
	s.mux.Handle("PUT /api/workspaces/{slug}/brain/definitions/{file}", authed(http.HandlerFunc(s.handleUpdateBrainDefinition)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/memories", authed(http.HandlerFunc(s.handleListMemories)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/memories/{memoryID}", authed(http.HandlerFunc(s.handleDeleteMemory)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/memories", authed(http.HandlerFunc(s.handleClearMemories)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/actions", authed(http.HandlerFunc(s.handleListActions)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/skills", authed(http.HandlerFunc(s.handleListSkills)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/skills/{file}", authed(http.HandlerFunc(s.handleGetSkill)))
	s.mux.Handle("PUT /api/workspaces/{slug}/brain/skills/{file}", authed(http.HandlerFunc(s.handleUpdateSkill)))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/skills/{file}", authed(http.HandlerFunc(s.handleDeleteSkill)))

	// Brain Knowledge (admin-only)
	s.mux.Handle("POST /api/workspaces/{slug}/brain/knowledge", authed(http.HandlerFunc(s.requireAdmin(s.handleCreateKnowledge))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/knowledge/upload", authed(http.HandlerFunc(s.requireAdmin(s.handleUploadKnowledge))))
	s.mux.Handle("POST /api/workspaces/{slug}/brain/knowledge/import-url", authed(http.HandlerFunc(s.requireAdmin(s.handleImportKnowledgeURL))))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/knowledge", authed(http.HandlerFunc(s.handleListKnowledge)))
	s.mux.Handle("GET /api/workspaces/{slug}/brain/knowledge/{id}", authed(http.HandlerFunc(s.handleGetKnowledge)))
	s.mux.Handle("PUT /api/workspaces/{slug}/brain/knowledge/{id}", authed(http.HandlerFunc(s.requireAdmin(s.handleUpdateKnowledge))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/brain/knowledge/{id}", authed(http.HandlerFunc(s.requireAdmin(s.handleDeleteKnowledge))))

	// Agents
	s.mux.Handle("POST /api/workspaces/{slug}/agents", authed(http.HandlerFunc(s.requireAdmin(s.handleCreateAgent))))
	s.mux.Handle("GET /api/workspaces/{slug}/agents", authed(http.HandlerFunc(s.handleListAgents)))
	s.mux.Handle("GET /api/workspaces/{slug}/agents/templates", authed(http.HandlerFunc(s.handleListAgentTemplates)))
	s.mux.Handle("GET /api/workspaces/{slug}/agents/{agentID}", authed(http.HandlerFunc(s.handleGetAgent)))
	s.mux.Handle("PUT /api/workspaces/{slug}/agents/{agentID}", authed(http.HandlerFunc(s.requireAdmin(s.handleUpdateAgent))))
	s.mux.Handle("DELETE /api/workspaces/{slug}/agents/{agentID}", authed(http.HandlerFunc(s.requireAdmin(s.handleDeleteAgent))))
	s.mux.Handle("POST /api/workspaces/{slug}/agents/generate", authed(http.HandlerFunc(s.requireAdmin(s.handleGenerateAgentConfig))))
	s.mux.Handle("POST /api/workspaces/{slug}/agents/from-template", authed(http.HandlerFunc(s.requireAdmin(s.handleCreateAgentFromTemplate))))

	// Org Chart
	s.mux.Handle("GET /api/workspaces/{slug}/org-chart", authed(http.HandlerFunc(s.handleGetOrgChart)))
	s.mux.Handle("PUT /api/workspaces/{slug}/org-chart/position", authed(http.HandlerFunc(s.requireAdmin(s.handleUpdateOrgPosition))))
	s.mux.Handle("PUT /api/workspaces/{slug}/members/{memberID}/profile", authed(http.HandlerFunc(s.handleUpdateMemberProfile)))

	// Org Roles
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
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Try to serve the file directly
		f, err := fsys.Open(path[1:]) // strip leading /
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fall back to index.html for SPA routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

func (s *Server) listenAndServe() error {
	srv := &http.Server{
		Handler:     s.mux,
		IdleTimeout: 120 * time.Second,
		// No ReadTimeout/WriteTimeout — they kill WebSocket connections
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	if s.cfg.Dev || s.cfg.Domain == "" {
		// Development mode: plain HTTP
		srv.Addr = s.cfg.Listen
		log.Printf("nexus dev server listening on %s", s.cfg.Listen)
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
		log.Printf("nexus server listening on :443 (domain: %s)", s.cfg.Domain)

		// HTTP→HTTPS redirect
		go http.ListenAndServe(":80", m.HTTPHandler(nil))
		go func() { errCh <- srv.ListenAndServeTLS("", "") }()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Println("shutting down...")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}
