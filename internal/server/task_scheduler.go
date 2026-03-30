package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/metrics"
)

// checkAgentTasks scans all workspaces for tasks with an agent_id and a
// scheduled_at time that has passed, then dispatches them to Brain or a custom
// agent and posts results to the task's channel.
func (s *Server) checkAgentTasks() {
	slugRows, err := s.global.DB.Query("SELECT slug FROM workspaces")
	if err != nil {
		return
	}
	var slugs []string
	for slugRows.Next() {
		var slug string
		slugRows.Scan(&slug)
		slugs = append(slugs, slug)
	}
	slugRows.Close()

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	for _, slug := range slugs {
		wdb, err := s.ws.Open(slug)
		if err != nil {
			continue
		}

		rows, err := wdb.DB.Query(
			`SELECT id, title, description, agent_id, COALESCE(channel_id,''), recurrence_rule, recurrence_end
			 FROM tasks
			 WHERE agent_id != '' AND status = 'in_progress' AND scheduled_at != '' AND scheduled_at <= ?`,
			nowStr,
		)
		if err != nil {
			continue
		}

		type pendingTask struct {
			id, title, description, agentID, channelID, recurrenceRule, recurrenceEnd string
		}
		var pending []pendingTask

		for rows.Next() {
			var pt pendingTask
			if err := rows.Scan(&pt.id, &pt.title, &pt.description, &pt.agentID, &pt.channelID, &pt.recurrenceRule, &pt.recurrenceEnd); err != nil {
				continue
			}
			pending = append(pending, pt)
		}
		rows.Close()

		for _, pt := range pending {
			// Resolve channel
			targetChannel := pt.channelID
			if targetChannel == "" {
				_ = wdb.DB.QueryRow("SELECT id FROM channels LIMIT 1").Scan(&targetChannel)
			}
			if targetChannel == "" {
				logger.WithCategory(logger.CatCalendar).Warn().Str("workspace", slug).Str("task", pt.id).Msg("no channel for agent task")
				continue
			}

			// Build instruction from description (primary) or title (fallback)
			var instruction string
			if pt.description != "" {
				instruction = pt.description
			} else {
				instruction = pt.title
			}

			// Create completion callback that records the run
			dispatchStart := time.Now()
			taskSlug := slug
			taskPt := pt
			taskChannel := targetChannel

			onComplete := func(msgID, response string, err error) {
				durationMS := int(time.Since(dispatchStart).Milliseconds())
				runStatus := "success"
				output := response
				if err != nil {
					runStatus = "error"
					if output == "" {
						output = err.Error()
					}
				}
				// Truncate output to 2000 chars
				if len(output) > 2000 {
					output = output[:2000]
				}

				wdb2, dbErr := s.ws.Open(taskSlug)
				if dbErr != nil {
					return
				}

				// Insert task_runs row
				_, _ = wdb2.DB.Exec(
					`INSERT INTO task_runs (id, task_id, status, output, message_id, channel_id, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					id.New(), taskPt.id, runStatus, output, msgID, taskChannel, durationMS,
				)

				// Update task's last_run_status
				_, _ = wdb2.DB.Exec(
					`UPDATE tasks SET last_run_status = ? WHERE id = ?`,
					runStatus, taskPt.id,
				)

				doneStr := time.Now().UTC().Format(time.RFC3339)

				if taskPt.recurrenceRule != "" {
					nextRun := advanceSchedule(now, taskPt.recurrenceRule)
					// Check if recurrence has ended
					expired := false
					if taskPt.recurrenceEnd != "" {
						endDate, parseErr := time.Parse("2006-01-02", taskPt.recurrenceEnd)
						if parseErr == nil && nextRun.After(endDate.Add(24*time.Hour)) {
							expired = true
						}
					}
					if expired {
						_, _ = wdb2.DB.Exec(
							`UPDATE tasks SET status = 'done', run_count = run_count + 1, last_run_at = ?, updated_at = ? WHERE id = ?`,
							doneStr, doneStr, taskPt.id,
						)
						logger.WithCategory(logger.CatCalendar).Info().Str("task", taskPt.title).Msg("recurring task completed (end date reached)")
					} else {
						// Reschedule — stay in_progress for next run (even on failure)
						_, _ = wdb2.DB.Exec(
							`UPDATE tasks SET scheduled_at = ?, run_count = run_count + 1, last_run_at = ?, updated_at = ? WHERE id = ?`,
							nextRun.Format(time.RFC3339), doneStr, doneStr, taskPt.id,
						)
						logger.WithCategory(logger.CatCalendar).Info().Str("task", taskPt.title).Str("next_run", nextRun.Format(time.RFC3339)).Str("status", runStatus).Msg("recurring task rescheduled")
					}
				} else {
					// One-shot: mark done
					_, _ = wdb2.DB.Exec(
						`UPDATE tasks SET status = 'done', run_count = run_count + 1, last_run_at = ?, updated_at = ? WHERE id = ?`,
						doneStr, doneStr, taskPt.id,
					)
				}
				s.broadcastTaskUpdate(taskSlug, taskPt.id)
			}

			// Dispatch with completion callback
			if pt.agentID == "brain" {
				logger.WithCategory(logger.CatCalendar).Info().Str("workspace", slug).Str("task", pt.title).Msg("executing agent task via Brain")
				s.executeBrainTask(slug, targetChannel, instruction, onComplete)
			} else {
				agent := s.loadAgentByID(slug, pt.agentID)
				if agent != nil {
					logger.WithCategory(logger.CatCalendar).Info().Str("workspace", slug).Str("agent", agent.Name).Str("task", pt.title).Msg("executing agent task")
					agentPrompt := fmt.Sprintf("[SCHEDULED TASK — execute this instruction, ignore previous channel messages]\n\n%s", instruction)
					s.handleAgentMentionEx(slug, targetChannel, "", "Task Scheduler", agentPrompt, agent, now, onComplete)
				} else {
					logger.WithCategory(logger.CatCalendar).Warn().Str("workspace", slug).Str("agent_id", pt.agentID).Msg("agent not found for task")
					// Fire callback with error for missing agent
					onComplete("", "", fmt.Errorf("agent %s not found", pt.agentID))
				}
			}
		}
	}
}

// advanceSchedule computes the next run time based on recurrence rule.
func advanceSchedule(from time.Time, rule string) time.Time {
	switch rule {
	case "hourly":
		return from.Add(1 * time.Hour)
	case "daily":
		return from.Add(24 * time.Hour)
	case "weekday":
		next := from.Add(24 * time.Hour)
		for next.Weekday() == time.Saturday || next.Weekday() == time.Sunday {
			next = next.Add(24 * time.Hour)
		}
		return next
	case "weekly":
		return from.Add(7 * 24 * time.Hour)
	default:
		return from.Add(24 * time.Hour)
	}
}

// executeBrainTask runs a scheduled task with a lean, task-focused prompt
// instead of the full chat system prompt. Brain gets SOUL.md personality,
// tool guidance, and the instruction — no workspace snapshot, channel history,
// or cross-channel context.
func (s *Server) executeBrainTask(slug, channelID, instruction string, onComplete TaskCompletionCallback) {
	go func() {
		var completionMsgID, completionResponse string
		var completionErr error
		defer func() {
			if onComplete != nil {
				onComplete(completionMsgID, completionResponse, completionErr)
			}
		}()

		// Acquire semaphore
		select {
		case s.agentSem <- struct{}{}:
			defer func() { <-s.agentSem }()
		default:
			s.agentSem <- struct{}{}
			defer func() { <-s.agentSem }()
		}

		metrics.AgentExecutionsTotal.WithLabelValues("Brain", "started").Inc()

		apiKey, model := s.getBrainSettings(slug)
		if apiKey == "" && s.getXAIKey(slug) == "" {
			completionErr = fmt.Errorf("no API key configured")
			return
		}

		// Build lean task execution prompt
		brainDir := brain.BrainDir(s.cfg.DataDir, slug)
		soulContent := brain.ReadDefinitionFile(brainDir, "SOUL.md")
		if soulContent == "" {
			soulContent = "You are Brain, a capable AI assistant."
		}

		now := time.Now().UTC()
		systemPrompt := soulContent + `

---

## Task Execution Mode
You are executing a scheduled task. Complete the instruction and post your result to this channel.

## Tool Usage
You have tools available — USE THEM to complete the task. Don't just describe what you would do, actually do it:
- Need to create a task? Call create_task.
- Need information? Call search_workspace or web_search.
- Need to fetch a URL? Call fetch_url.
- Need to write something? Call create_document.
- Need to notify someone? Call send_email or send_telegram.
- Need an image? Call generate_image.
- Need to delegate? Call delegate_to_agent.

Act, don't narrate. Execute the instruction fully, then summarize what you did.

## Current Time
UTC: ` + now.Format(time.RFC3339) + `
Day: ` + now.Format("Monday, January 2, 2006")

		// Inject North Star context if set
		if nsCtx := s.buildNorthStarContext(slug); nsCtx != "" {
			systemPrompt += "\n\n---\n\n" + nsCtx
		}

		// Broadcast thinking state
		s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "")
		defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "")

		messages := []brain.Message{
			{Role: "user", Content: instruction},
		}

		resolvedModel, fallbacks := s.resolveFreeAuto(model, slug)
		client := s.makeBrainClient(slug, apiKey, resolvedModel, fallbacks)

		allTools := s.getAllTools(slug)
		responseContent, toolCalls, usage, err := client.CompleteWithTools(systemPrompt, messages, allTools)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("task execution LLM error")
			completionErr = err
			return
		}
		s.trackUsage(slug, usage, resolvedModel, "task", channelID, "")

		if len(toolCalls) == 0 {
			responseContent = strings.TrimSpace(responseContent)
			if responseContent != "" {
				completionMsgID = s.sendBrainMessage(slug, channelID, "", responseContent)
			}
			completionResponse = responseContent
			return
		}

		// Execute tool calls
		assistantMsg := brain.Message{Role: "assistant", Content: responseContent, ToolCalls: toolCalls}
		followUp := append(messages, assistantMsg)

		var toolNames []string
		var imageRefs []string
		var toolResults []string
		for _, call := range toolCalls {
			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "tool_executing", call.Function.Name)
			result := s.executeTool(slug, channelID, "", call)
			toolNames = append(toolNames, call.Function.Name)
			imageRefs = append(imageRefs, extractImageMarkdown(result)...)
			toolResults = append(toolResults, result)
			followUp = append(followUp, brain.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
			})
		}

		// Check ResultAsAnswer
		if len(toolCalls) == 1 {
			if td := findToolDef(allTools, toolCalls[0].Function.Name); td != nil && td.Function.ResultAsAnswer {
				finalResponse := appendMissingImages(toolResults[0], imageRefs)
				if finalResponse != "" {
					completionMsgID = s.sendBrainMessage(slug, channelID, "", finalResponse, toolNames...)
				}
				completionResponse = finalResponse
				return
			}
		}

		// Follow-up call with tool results
		finalResponse, usage2, err := client.Complete(systemPrompt, followUp)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("task follow-up LLM error")
			completionErr = err
			if responseContent != "" {
				completionMsgID = s.sendBrainMessage(slug, channelID, "", appendMissingImages(responseContent, imageRefs))
				completionResponse = responseContent
			}
			return
		}
		s.trackUsage(slug, usage2, resolvedModel, "task", channelID, "")

		finalResponse = strings.TrimSpace(finalResponse)
		finalResponse = appendMissingImages(finalResponse, imageRefs)
		if finalResponse != "" {
			completionMsgID = s.sendBrainMessage(slug, channelID, "", finalResponse, toolNames...)
		}
		completionResponse = finalResponse
	}()
}

// broadcastTaskUpdate re-reads a task and broadcasts it via the hub.
func (s *Server) broadcastTaskUpdate(slug, taskID string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}
	t, err := scanTask(wdb.DB.QueryRow(
		"SELECT "+taskSelectCols+" FROM tasks WHERE id = ?", taskID,
	))
	if err != nil {
		return
	}

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskUpdated, t), "")
}
