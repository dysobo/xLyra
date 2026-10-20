package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	oauthsvc "xlyra/server/internal/oauth"
	sitepkg "xlyra/server/internal/site"
)

type siteWorkBuddyAccountUpdateRequest struct {
	Enabled *bool `json:"enabled"`
}

type workBuddyLoginStartRequest struct {
	// Region selects the WorkBuddy site to log in to; empty means the default.
	Region string `json:"region"`
}

type workBuddyImportRequest struct {
	Files []sitepkg.WorkBuddyImportDocument `json:"files"`
}

type workBuddyLoginPollRequest struct {
	State        string     `json:"state"`
	CredentialID *uuid.UUID `json:"credential_id"`
}

func (h Handler) ListWorkBuddyAccounts(w http.ResponseWriter, r *http.Request) {
	if h.sites == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "site_service_unavailable", "site service is not available")
		return
	}
	siteID, ok := siteIDParam(w, r)
	if !ok {
		return
	}
	accounts, err := h.sites.ListWorkBuddyAccounts(r.Context(), siteID)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "workbuddy_accounts_list_failed", err.Error())
		return
	}
	h.writeItems(w, http.StatusOK, accounts, map[string]any{"count": len(accounts)})
}

func (h Handler) UpdateWorkBuddyAccount(w http.ResponseWriter, r *http.Request) {
	if h.sites == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "site_service_unavailable", "site service is not available")
		return
	}
	siteID, ok := siteIDParam(w, r)
	if !ok {
		return
	}
	credentialID, ok := grokCredentialIDParam(w, r)
	if !ok {
		return
	}
	var payload siteWorkBuddyAccountUpdateRequest
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	account, err := h.sites.UpdateWorkBuddyAccount(r.Context(), siteID, credentialID, sitepkg.WorkBuddyAccountUpdate{Enabled: payload.Enabled})
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "workbuddy_account_update_failed", err.Error())
		return
	}
	h.invalidateGatewayModelsCache()
	h.writeResource(w, http.StatusOK, "account", account)
}

func (h Handler) UpdateWorkBuddyAccountModel(w http.ResponseWriter, r *http.Request) {
	if h.sites == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "site_service_unavailable", "site service is not available")
		return
	}
	siteID, ok := siteIDParam(w, r)
	if !ok {
		return
	}
	credentialID, ok := grokCredentialIDParam(w, r)
	if !ok {
		return
	}
	// Same shape as the API key model update: toggle a model and/or adjust the
	// protocols the account may use for it.
	var payload struct {
		Model         string   `json:"model"`
		SiteModelID   string   `json:"site_model_id"`
		Enabled       *bool    `json:"enabled"`
		EndpointMode  string   `json:"endpoint_mode"`
		EndpointTypes []string `json:"endpoint_types"`
	}
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	var siteModelID uuid.UUID
	if strings.TrimSpace(payload.SiteModelID) != "" {
		parsed, err := uuid.Parse(payload.SiteModelID)
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "invalid_site_model_id", "site_model_id must be a valid UUID")
			return
		}
		siteModelID = parsed
	}
	var account any
	if strings.TrimSpace(payload.Model) != "" && payload.Enabled != nil {
		updated, err := h.sites.SetWorkBuddyAccountModelEnabled(r.Context(), siteID, credentialID, payload.Model, *payload.Enabled)
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "workbuddy_account_model_update_failed", err.Error())
			return
		}
		account = updated
	}
	if siteModelID != uuid.Nil && (payload.EndpointMode != "" || payload.EndpointTypes != nil) {
		updated, err := h.sites.SetWorkBuddyAccountModelEndpoint(r.Context(), siteID, credentialID, siteModelID, payload.EndpointMode, payload.EndpointTypes)
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "workbuddy_account_model_endpoint_update_failed", err.Error())
			return
		}
		account = updated
	}
	if account == nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_model_update", "model with enabled, or site_model_id with endpoint_mode/endpoint_types is required")
		return
	}
	h.invalidateGatewayModelsCache()
	h.writeResource(w, http.StatusOK, "account", account)
}

func (h Handler) DeleteWorkBuddyAccount(w http.ResponseWriter, r *http.Request) {
	if h.sites == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "site_service_unavailable", "site service is not available")
		return
	}
	siteID, ok := siteIDParam(w, r)
	if !ok {
		return
	}
	credentialID, ok := grokCredentialIDParam(w, r)
	if !ok {
		return
	}
	if err := h.sites.DeleteWorkBuddyAccount(r.Context(), siteID, credentialID); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "workbuddy_account_delete_failed", err.Error())
		return
	}
	h.invalidateGatewayModelsCache()
	w.WriteHeader(http.StatusNoContent)
}

func (h Handler) RefreshWorkBuddyAccount(w http.ResponseWriter, r *http.Request) {
	if h.sites == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "site_service_unavailable", "site service is not available")
		return
	}
	siteID, ok := siteIDParam(w, r)
	if !ok {
		return
	}
	credentialID, ok := grokCredentialIDParam(w, r)
	if !ok {
		return
	}
	account, err := h.sites.RefreshWorkBuddyAccount(r.Context(), siteID, credentialID)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "workbuddy_account_refresh_failed", err.Error())
		return
	}
	h.invalidateGatewayModelsCache()
	h.writeResource(w, http.StatusOK, "account", account)
}

func (h Handler) RefreshWorkBuddyAccounts(w http.ResponseWriter, r *http.Request) {
	if h.sites == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "site_service_unavailable", "site service is not available")
		return
	}
	siteID, ok := siteIDParam(w, r)
	if !ok {
		return
	}
	results, err := h.sites.RefreshWorkBuddyAccounts(r.Context(), siteID)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "workbuddy_accounts_refresh_failed", err.Error())
		return
	}
	accounts, listErr := h.sites.ListWorkBuddyAccounts(r.Context(), siteID)
	if listErr != nil {
		h.writeError(w, r, http.StatusInternalServerError, "workbuddy_accounts_list_failed", listErr.Error())
		return
	}
	failed := 0
	for _, result := range results {
		if result.Error != "" {
			failed++
		}
	}
	h.invalidateGatewayModelsCache()
	h.writeItems(w, http.StatusOK, accounts, map[string]any{"count": len(accounts), "failed": failed})
}

// StartWorkBuddyLogin begins the browser login: it returns the URL the user has
// to open plus the state the client polls with.
func (h Handler) StartWorkBuddyLogin(w http.ResponseWriter, r *http.Request) {
	if h.oauth == nil || h.sites == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "oauth_service_unavailable", "oauth service is not available")
		return
	}
	siteID, ok := siteIDParam(w, r)
	if !ok {
		return
	}
	var payload workBuddyLoginStartRequest
	// The body is optional: clients that send none log in to the default site.
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	login, err := h.oauth.StartWorkBuddyLogin(r.Context(), siteID, payload.Region)
	if err != nil {
		h.writeError(w, r, http.StatusBadGateway, "workbuddy_login_start_failed", err.Error())
		return
	}
	h.writeResource(w, http.StatusOK, "login", map[string]any{
		"region":     login.Region,
		"state":      login.State,
		"auth_url":   login.AuthURL,
		"interval":   login.Interval,
		"expires_at": login.ExpiresAt,
	})
}

func (h Handler) PollWorkBuddyLogin(w http.ResponseWriter, r *http.Request) {
	if h.oauth == nil || h.sites == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "oauth_service_unavailable", "oauth service is not available")
		return
	}
	siteID, ok := siteIDParam(w, r)
	if !ok {
		return
	}
	var payload workBuddyLoginPollRequest
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.State) == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_state", "state is required")
		return
	}
	tokens, err := h.oauth.PollWorkBuddyLogin(r.Context(), siteID, payload.State)
	if err != nil {
		if errors.Is(err, oauthsvc.ErrWorkBuddyAuthorizationPending) {
			h.writeResource(w, http.StatusOK, "login", map[string]any{"status": "pending"})
			return
		}
		h.writeError(w, r, http.StatusBadRequest, "workbuddy_login_poll_failed", err.Error())
		return
	}
	account, err := h.sites.CompleteWorkBuddyLogin(r.Context(), siteID, tokens, payload.CredentialID)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "workbuddy_account_create_failed", err.Error())
		return
	}
	if refreshed, refreshErr := h.sites.RefreshWorkBuddyAccount(r.Context(), siteID, account.CredentialID); refreshErr == nil {
		account = refreshed
	}
	h.invalidateGatewayModelsCache()
	h.writeResource(w, http.StatusOK, "login", map[string]any{"status": "complete", "account": account})
}

// ListWorkBuddyRegions returns the WorkBuddy sites (China / international) an
// account can be logged in to, as configured in protocol_specs.json.
func (h Handler) ListWorkBuddyRegions(w http.ResponseWriter, r *http.Request) {
	regions := sitepkg.WorkBuddyRegions()
	items := make([]map[string]any, 0, len(regions))
	for _, region := range regions {
		items = append(items, map[string]any{
			"id":       region.ID,
			"label":    region.Label,
			"label_en": region.LabelEN,
		})
	}
	h.writeItems(w, http.StatusOK, items, map[string]any{
		"count":   len(items),
		"default": sitepkg.WorkBuddyDefaultRegionID(),
	})
}

// ImportWorkBuddyAccounts stores the accounts of uploaded export files.
func (h Handler) ImportWorkBuddyAccounts(w http.ResponseWriter, r *http.Request) {
	if h.sites == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "site_service_unavailable", "site service is not available")
		return
	}
	siteID, ok := siteIDParam(w, r)
	if !ok {
		return
	}
	var payload workBuddyImportRequest
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	items, err := h.sites.ImportWorkBuddyAccounts(r.Context(), siteID, payload.Files)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "workbuddy_import_failed", err.Error())
		return
	}
	imported, failed := 0, 0
	for _, item := range items {
		if item.Status == "imported" {
			imported++
		} else {
			failed++
		}
	}
	h.invalidateGatewayModelsCache()
	h.writeItems(w, http.StatusOK, items, map[string]any{"total": len(items), "imported": imported, "failed": failed})
}
