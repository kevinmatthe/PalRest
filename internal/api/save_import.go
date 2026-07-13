package api

import (
	"net/http"
)

func (s *Server) importSaveSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	cfg := s.config()
	if !cfg.Save.Enabled || s.saveImporter == nil {
		writeError(w, http.StatusNotFound, "not_found", "save import is not enabled")
		return
	}
	result, err := s.saveImporter.Import(r.Context(), cfg.Save.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save_import_failed", "save import failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"import_id":   result.ImportID,
		"fingerprint": result.Fingerprint,
		"inserted":    result.Inserted,
	})
}
