package brain3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// ErrSessionNotFound is returned by LookupSession when no record exists for
// the (channel_id, parent_id) pair.
var ErrSessionNotFound = errors.New("brain3: session not found")

// LookupSession returns the persisted Anthropic session for a channel-thread
// pair, or ErrSessionNotFound if none exists. parent_id is "" for the
// channel root and the parent message ID for thread replies.
func LookupSession(db *sql.DB, channelID, parentID string) (SessionRecord, error) {
	var rec SessionRecord
	row := db.QueryRow(`
		SELECT channel_id, parent_id, anthropic_session_id, status,
		       created_at, updated_at, last_event_at
		FROM brain_managed_sessions
		WHERE channel_id = ? AND parent_id = ?
		LIMIT 1
	`, channelID, parentID)

	var created, updated, lastEvent string
	if err := row.Scan(
		&rec.ChannelID, &rec.ParentID, &rec.AnthropicSessionID, &rec.Status,
		&created, &updated, &lastEvent,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionRecord{}, ErrSessionNotFound
		}
		return SessionRecord{}, err
	}
	rec.CreatedAt, _ = time.Parse(time.RFC3339, created)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	rec.LastEventAt, _ = time.Parse(time.RFC3339, lastEvent)
	return rec, nil
}

// SaveSession upserts the session record for a channel-thread pair.
func SaveSession(db *sql.DB, rec SessionRecord) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	_, err := db.Exec(`
		INSERT INTO brain_managed_sessions
			(channel_id, parent_id, anthropic_session_id, status,
			 created_at, updated_at, last_event_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id, parent_id) DO UPDATE SET
			anthropic_session_id = excluded.anthropic_session_id,
			status               = excluded.status,
			updated_at           = excluded.updated_at,
			last_event_at        = excluded.last_event_at
	`,
		rec.ChannelID, rec.ParentID, rec.AnthropicSessionID, rec.Status,
		rec.CreatedAt.Format(time.RFC3339), now, now,
	)
	return err
}

// MarkTerminated flags a session as terminated so future turns provision a
// fresh one instead of reusing it.
func MarkTerminated(db *sql.DB, channelID, parentID string) error {
	_, err := db.Exec(`
		UPDATE brain_managed_sessions
		SET status = 'terminated', updated_at = ?
		WHERE channel_id = ? AND parent_id = ?
	`, time.Now().UTC().Format(time.RFC3339), channelID, parentID)
	return err
}

// EnsureSession returns an active Anthropic session ID for a (channel_id,
// parent_id) pair, creating one if needed. New sessions reference the
// workspace's pre-provisioned agent (latest version) and environment, and
// mount the memory_store as a read-write resource so the agent can use the
// FUSE mount at /mnt/memory/brain/.
//
// If a persisted session exists but is terminated, it's marked stale and a
// fresh one is provisioned in its place.
func EnsureSession(ctx context.Context, client *anthropic.Client, db *sql.DB, info AgentInfo, channelID, parentID string) (string, error) {
	rec, err := LookupSession(db, channelID, parentID)
	if err == nil && rec.IsActive() {
		return rec.AnthropicSessionID, nil
	}
	if err != nil && !errors.Is(err, ErrSessionNotFound) {
		return "", fmt.Errorf("lookup session: %w", err)
	}

	// No usable session — create one.
	createCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resources := []anthropic.BetaSessionNewParamsResourceUnion{}
	if info.MemoryStoreID != "" {
		resources = append(resources, anthropic.BetaSessionNewParamsResourceUnion{
			OfMemoryStore: &anthropic.BetaManagedAgentsMemoryStoreResourceParam{
				MemoryStoreID: info.MemoryStoreID,
				Type:          anthropic.BetaManagedAgentsMemoryStoreResourceParamTypeMemoryStore,
				Access:        anthropic.BetaManagedAgentsMemoryStoreResourceParamAccessReadWrite,
			},
		})
	}

	title := "Nexus channel " + channelID
	if parentID != "" {
		title = "Nexus thread " + parentID
	}

	sess, err := client.Beta.Sessions.New(createCtx, anthropic.BetaSessionNewParams{
		Agent:         anthropic.BetaSessionNewParamsAgentUnion{OfString: param.NewOpt(info.AgentID)},
		EnvironmentID: info.EnvironmentID,
		Title:         param.NewOpt(title),
		Resources:     resources,
	})
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	if err := SaveSession(db, SessionRecord{
		ChannelID:          channelID,
		ParentID:           parentID,
		AnthropicSessionID: sess.ID,
		Status:             "idle",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		return "", fmt.Errorf("persist session: %w", err)
	}
	return sess.ID, nil
}
