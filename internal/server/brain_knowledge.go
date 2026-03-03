package server

import (
	"bytes"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
	"golang.org/x/net/html"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/id"
)

type knowledgeArticle struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Content    string `json:"content,omitempty"`
	SourceType string `json:"source_type"`
	SourceName string `json:"source_name,omitempty"`
	Tokens     int    `json:"tokens"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
}

type createKnowledgeReq struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (s *Server) handleCreateKnowledge(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)

	var req createKnowledgeReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "title and content required")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	articleID := id.New()
	tokens := len(req.Content) / 4

	_, err = wdb.DB.Exec(
		"INSERT INTO brain_knowledge (id, title, content, source_type, tokens, created_by) VALUES (?, ?, ?, 'text', ?, ?)",
		articleID, req.Title, req.Content, tokens, claims.UserID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create article")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"id": articleID})
}

func (s *Server) handleUploadKnowledge(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)

	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB max
		writeError(w, http.StatusBadRequest, "file too large")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "no file provided")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".txt" && ext != ".md" && ext != ".pdf" {
		writeError(w, http.StatusBadRequest, "only .txt, .md, and .pdf files supported")
		return
	}

	var content string
	sourceType := "file"

	if ext == ".pdf" {
		// Read PDF into memory for parsing
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read file")
			return
		}
		reader := bytes.NewReader(data)
		pdfReader, err := pdf.NewReader(reader, int64(len(data)))
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse PDF: "+err.Error())
			return
		}
		var buf strings.Builder
		for i := 1; i <= pdfReader.NumPage(); i++ {
			p := pdfReader.Page(i)
			if p.V.IsNull() {
				continue
			}
			text, err := p.GetPlainText(nil)
			if err != nil {
				continue
			}
			buf.WriteString(text)
			buf.WriteString("\n")
		}
		content = strings.TrimSpace(buf.String())
		if content == "" {
			writeError(w, http.StatusBadRequest, "no text content found in PDF")
			return
		}
		sourceType = "pdf"
	} else {
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read file")
			return
		}
		content = string(data)
	}

	title := strings.TrimSuffix(header.Filename, ext)
	tokens := len(content) / 4

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	articleID := id.New()
	_, err = wdb.DB.Exec(
		"INSERT INTO brain_knowledge (id, title, content, source_type, source_name, tokens, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)",
		articleID, title, content, sourceType, header.Filename, tokens, claims.UserID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store article")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"id": articleID})
}

func (s *Server) handleListKnowledge(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	rows, err := wdb.DB.Query("SELECT id, title, source_type, COALESCE(source_name, ''), tokens, created_by, created_at FROM brain_knowledge ORDER BY created_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list knowledge")
		return
	}
	defer rows.Close()

	var articles []knowledgeArticle
	for rows.Next() {
		var a knowledgeArticle
		if err := rows.Scan(&a.ID, &a.Title, &a.SourceType, &a.SourceName, &a.Tokens, &a.CreatedBy, &a.CreatedAt); err != nil {
			continue
		}
		articles = append(articles, a)
	}

	if articles == nil {
		articles = []knowledgeArticle{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": articles})
}

func (s *Server) handleGetKnowledge(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	articleID := r.PathValue("id")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var a knowledgeArticle
	err = wdb.DB.QueryRow(
		"SELECT id, title, content, source_type, COALESCE(source_name, ''), tokens, created_by, created_at FROM brain_knowledge WHERE id = ?",
		articleID,
	).Scan(&a.ID, &a.Title, &a.Content, &a.SourceType, &a.SourceName, &a.Tokens, &a.CreatedBy, &a.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleUpdateKnowledge(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	articleID := r.PathValue("id")

	var req createKnowledgeReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	tokens := len(req.Content) / 4
	res, err := wdb.DB.Exec(
		"UPDATE brain_knowledge SET title = ?, content = ?, tokens = ? WHERE id = ?",
		req.Title, req.Content, tokens, articleID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteKnowledge(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	articleID := r.PathValue("id")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	res, err := wdb.DB.Exec("DELETE FROM brain_knowledge WHERE id = ?", articleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleImportKnowledgeURL fetches a URL and extracts readable text for preview.
func (s *Server) handleImportKnowledgeURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &req); err != nil || req.URL == "" {
		writeError(w, http.StatusBadRequest, "url required")
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(req.URL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch URL: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// Limit to 5MB
	limited := io.LimitReader(resp.Body, 5*1024*1024)
	body, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to read response")
		return
	}

	// Extract text from HTML
	title, content := extractHTMLText(string(body))
	if title == "" {
		title = req.URL
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"title":   title,
		"content": content,
		"url":     req.URL,
	})
}

// extractHTMLText strips HTML tags and returns title + body text.
func extractHTMLText(rawHTML string) (string, string) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "", rawHTML
	}

	var title string
	var textBuf strings.Builder
	inTitle := false
	inScript := false
	inStyle := false

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				inTitle = true
			case "script", "noscript":
				inScript = true
			case "style":
				inStyle = true
			case "br", "p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr":
				textBuf.WriteString("\n")
			}
		}
		if n.Type == html.TextNode && !inScript && !inStyle {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if inTitle {
					title = text
				} else {
					textBuf.WriteString(text)
					textBuf.WriteString(" ")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				inTitle = false
			case "script", "noscript":
				inScript = false
			case "style":
				inStyle = false
			}
		}
	}
	walk(doc)

	// Clean up excessive whitespace
	content := textBuf.String()
	lines := strings.Split(content, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return title, strings.Join(cleaned, "\n")
}

