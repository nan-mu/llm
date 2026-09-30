package httpx

import (
	"strings"

	"encore.app/internal/modelstate"
	"encore.app/unix"
)

type modelsListResponse struct {
	Data []modelItem `json:"data"`
}

type modelItem struct {
	ID     string      `json:"id"`
	Path   string      `json:"path"`
	Status modelStatus `json:"status"`
}

type modelStatus struct {
	Value    string `json:"value"`
	Failed   bool   `json:"failed"`
	ExitCode int    `json:"exit_code"`
}

func (m modelItem) toModel() unix.Model {
	return unix.Model{
		ID:    m.ID,
		Path:  m.Path,
		State: parseState(m.Status),
	}
}

func parseState(st modelStatus) modelstate.ModelState {
	if st.Failed {
		return modelstate.ModelFailed
	}
	switch strings.ToLower(strings.TrimSpace(st.Value)) {
	case "unloaded":
		return modelstate.ModelUnloaded
	case "loading":
		return modelstate.ModelLoading
	case "loaded", "sleeping":
		return modelstate.ModelLoaded
	case "unloading":
		return modelstate.ModelUnloading
	default:
		return modelstate.ModelFailed
	}
}
