package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/nexus-chat/nexus/internal/logger"
)

// Async task type constants.
const (
	TaskExtractMemories    = "brain:extract_memories"
	TaskUpdateSummary      = "brain:update_summary"
	TaskReindex            = "search:reindex"
	TaskSendEventReminder  = "calendar:send_reminder"
	TaskSendEventInvite    = "calendar:send_invite"
)

// ExtractMemoriesPayload is the payload for memory extraction tasks.
type ExtractMemoriesPayload struct {
	Slug      string `json:"slug"`
	ChannelID string `json:"channel_id"`
}

// UpdateSummaryPayload is the payload for channel summary update tasks.
type UpdateSummaryPayload struct {
	Slug      string `json:"slug"`
	ChannelID string `json:"channel_id"`
}

// ReindexPayload is the payload for search reindex tasks.
type ReindexPayload struct {
	Slug string `json:"slug"`
}

// enqueueTask enqueues a task via asynq if available, otherwise falls back to a goroutine.
func (s *Server) enqueueTask(taskType string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Str("task_type", taskType).Msg("failed to marshal task payload")
		return
	}

	if s.asynqClient != nil {
		task := asynq.NewTask(taskType, data,
			asynq.MaxRetry(3),
			asynq.Unique(30*time.Second),
		)
		if _, err := s.asynqClient.Enqueue(task); err != nil {
			logger.WithCategory(logger.CatSystem).Error().Err(err).Str("task_type", taskType).Msg("failed to enqueue task")
		}
		return
	}

	// Fallback: run in goroutine
	go s.handleAsyncTask(context.Background(), taskType, data)
}

// handleAsyncTask dispatches an async task by type.
func (s *Server) handleAsyncTask(_ context.Context, taskType string, data []byte) {
	switch taskType {
	case TaskExtractMemories:
		var p ExtractMemoriesPayload
		if json.Unmarshal(data, &p) == nil {
			s.extractMemories(p.Slug, p.ChannelID)
		}
	case TaskUpdateSummary:
		var p UpdateSummaryPayload
		if json.Unmarshal(data, &p) == nil {
			s.updateChannelSummary(p.Slug, p.ChannelID)
		}
	case TaskReindex:
		var p ReindexPayload
		if json.Unmarshal(data, &p) == nil {
			logger.WithCategory(logger.CatSystem).Info().Str("workspace", p.Slug).Msg("reindex task not yet implemented")
		}
	default:
		logger.WithCategory(logger.CatSystem).Warn().Str("task_type", taskType).Msg("unknown task type")
	}
}

// asynqHandlerFunc wraps handleAsyncTask for the asynq mux.
func (s *Server) asynqHandlerFunc(taskType string) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		s.handleAsyncTask(ctx, taskType, t.Payload())
		return nil
	}
}

// initAsynq sets up the asynq client and server if RedisURL is configured.
func (s *Server) initAsynq() error {
	if s.cfg.RedisURL == "" {
		logger.WithCategory(logger.CatSystem).Info().Msg("no REDIS_URL configured, using goroutine fallback for tasks")
		return nil
	}

	opt, err := asynq.ParseRedisURI(s.cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("parsing redis URL: %w", err)
	}

	s.asynqClient = asynq.NewClient(opt)

	srv := asynq.NewServer(opt, asynq.Config{
		Concurrency: 5,
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskExtractMemories, s.asynqHandlerFunc(TaskExtractMemories))
	mux.HandleFunc(TaskUpdateSummary, s.asynqHandlerFunc(TaskUpdateSummary))
	mux.HandleFunc(TaskReindex, s.asynqHandlerFunc(TaskReindex))

	s.asynqServer = srv
	go func() {
		if err := srv.Run(mux); err != nil {
			logger.WithCategory(logger.CatSystem).Error().Err(err).Msg("asynq server error")
		}
	}()

	logger.WithCategory(logger.CatSystem).Info().Int("concurrency", 5).Msg("asynq worker started")
	return nil
}
