package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xlyra/server/internal/adapter"
	oauthsvc "xlyra/server/internal/oauth"
	"xlyra/server/internal/store"
)

// WorkBuddy accounts mirror the Grok account model: a site of type "workbuddy"
// owns one credential per logged-in account, stored as an encrypted token
// bundle under credential type "workbuddy_oauth:<uuid>".
const workBuddyCredentialPrefix = "workbuddy_oauth:"

type WorkBuddyAccountUpdate struct {
	Enabled *bool
}

type WorkBuddyAccount struct {
	CredentialID uuid.UUID `json:"credential_id"`
	MaskedToken  string    `json:"masked_token"`
	Enabled      bool      `json:"enabled"`
	// Region is the WorkBuddy site the account is logged in to ("cn" or "intl"),
	// as configured in protocol_specs.json.
	Region      string             `json:"region"`
	SyncStatus  string             `json:"sync_status,omitempty"`
	SyncMessage string             `json:"sync_message,omitempty"`
	ModelCount  int                `json:"model_count"`
	Models      []GrokAccountModel `json:"models"`
	// Quota is reported in credits; QuotaLimit/Packages come from the last
	// refresh (zero/empty until the account has been refreshed once).
	RemainingQuota *int64                  `json:"remaining_quota,omitempty"`
	UsedQuota      *int64                  `json:"used_quota,omitempty"`
	QuotaLimit     *int64                  `json:"quota_limit,omitempty"`
	QuotaUnit      string                  `json:"quota_unit,omitempty"`
	QuotaPackages  []WorkBuddyQuotaPackage `json:"quota_packages,omitempty"`
}

// WorkBuddyQuotaPackage is one credit package of an account, kept in
// fractional credits as reported by the billing API.
type WorkBuddyQuotaPackage struct {
	Name      string  `json:"name"`
	Size      float64 `json:"size"`
	Remaining float64 `json:"remaining"`
	Recurring bool    `json:"recurring"`
	CycleEnd  string  `json:"cycle_end,omitempty"`
	ExpiresAt string  `json:"expires_at,omitempty"`
}

type WorkBuddyRefreshBatchItem struct {
	CredentialID uuid.UUID         `json:"credential_id"`
	Account      *WorkBuddyAccount `json:"account,omitempty"`
	Error        string            `json:"error,omitempty"`
}

func isWorkBuddyCredentialType(value string) bool {
	if !strings.HasPrefix(value, workBuddyCredentialPrefix) {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(value, workBuddyCredentialPrefix))
	return err == nil
}

func isWorkBuddySiteType(siteType string) bool {
	return strings.EqualFold(strings.TrimSpace(siteType), adapter.WorkBuddySiteType)
}

func (s *Service) requireWorkBuddySite(ctx context.Context, siteID uuid.UUID) error {
	if siteID == uuid.Nil {
		return fmt.Errorf("site id is required")
	}
	item, err := s.Get(ctx, siteID)
	if err != nil {
		return err
	}
	if !isWorkBuddySiteType(item.SiteType) {
		return fmt.Errorf("site type %q is not workbuddy", item.SiteType)
	}
	return nil
}

func (s *Service) ListWorkBuddyAccounts(ctx context.Context, siteID uuid.UUID) ([]WorkBuddyAccount, error) {
	if err := s.requireWorkBuddySite(ctx, siteID); err != nil {
		return nil, err
	}
	credentials, err := store.NewSiteCredentialRepository(s.db.DB()).ListBySite(ctx, siteID)
	if err != nil {
		return nil, err
	}
	states, err := store.NewSiteAPIKeyStateRepository(s.db.DB()).ListBySite(ctx, siteID)
	if err != nil {
		return nil, err
	}
	models, err := store.NewSiteAPIKeyModelRepository(s.db.DB()).ListBySite(ctx, siteID)
	if err != nil {
		return nil, err
	}
	statesByCredential := make(map[uuid.UUID]store.SiteAPIKeyState, len(states))
	for _, state := range states {
		statesByCredential[state.SiteCredentialID] = state
	}
	modelsByCredential := map[uuid.UUID][]store.SiteAPIKeyModel{}
	for _, model := range models {
		modelsByCredential[model.SiteCredentialID] = append(modelsByCredential[model.SiteCredentialID], model)
	}
	accounts := make([]WorkBuddyAccount, 0, len(credentials))
	for _, credential := range credentials {
		if account, ok := workBuddyAccountFromStore(credential, statesByCredential[credential.ID], modelsByCredential[credential.ID]); ok {
			account.Models = s.resolveAccountModelEndpoints(ctx, account.Models)
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (s *Service) UpdateWorkBuddyAccount(ctx context.Context, siteID uuid.UUID, credentialID uuid.UUID, input WorkBuddyAccountUpdate) (WorkBuddyAccount, error) {
	if err := s.requireWorkBuddySite(ctx, siteID); err != nil {
		return WorkBuddyAccount{}, err
	}
	var updated store.SiteCredential
	err := s.db.WithinTx(ctx, func(tx store.Tx) error {
		credentialRepo := store.NewSiteCredentialRepository(tx)
		stateRepo := store.NewSiteAPIKeyStateRepository(tx)
		credential, err := credentialRepo.GetByID(ctx, credentialID)
		if err != nil {
			return err
		}
		if credential.SiteID != siteID || !isWorkBuddyCredentialType(credential.CredentialType) {
			return fmt.Errorf("workbuddy account was not found")
		}
		meta := grokMetaMap(credential.Meta)
		enable := input.Enabled != nil && *input.Enabled
		disable := input.Enabled != nil && !*input.Enabled
		if input.Enabled != nil {
			if enable && boolFromMeta(meta, "reauth_required", false) {
				return fmt.Errorf("workbuddy account requires reauthorization")
			}
			meta["enabled"] = *input.Enabled
		}
		encoded, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("marshal workbuddy account meta: %w", err)
		}
		updated, err = credentialRepo.UpdateMeta(ctx, credential.ID, encoded)
		if err != nil {
			return err
		}
		if enable {
			if err := store.NewRouteCooldownRepository(tx).DeleteBySiteCredential(ctx, siteID, credentialID); err != nil {
				return err
			}
		}
		if enable || disable {
			states, err := stateRepo.ListBySite(ctx, siteID)
			if err != nil {
				return err
			}
			for _, state := range states {
				if state.SiteCredentialID == credentialID {
					if enable {
						_, err = stateRepo.RecoverPending(ctx, credentialID)
					} else {
						_, err = stateRepo.UpdateEnabled(ctx, credentialID, false)
					}
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	s.syncWorkBuddyQuotaSummary(ctx, siteID, store.Site{})
	return s.workBuddyAccountSnapshot(ctx, updated), nil
}

func (s *Service) SetWorkBuddyAccountModelEnabled(ctx context.Context, siteID uuid.UUID, credentialID uuid.UUID, modelName string, enabled bool) (WorkBuddyAccount, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return WorkBuddyAccount{}, fmt.Errorf("model name is required")
	}
	if err := s.requireWorkBuddySite(ctx, siteID); err != nil {
		return WorkBuddyAccount{}, err
	}
	defer s.siteLocks.lock(siteID)()
	credentialRepo := store.NewSiteCredentialRepository(s.db.DB())
	credential, err := credentialRepo.GetByID(ctx, credentialID)
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	if credential.SiteID != siteID || !isWorkBuddyCredentialType(credential.CredentialType) {
		return WorkBuddyAccount{}, fmt.Errorf("workbuddy account was not found")
	}
	meta := grokMetaMap(credential.Meta)
	disabledModels := stringSetFromAny(meta["disabled_models"])
	if enabled {
		delete(disabledModels, modelName)
	} else {
		disabledModels[modelName] = struct{}{}
	}
	meta["disabled_models"] = sortedStringSet(disabledModels)
	updatedAPIKeyModel, _ := store.NewSiteAPIKeyModelRepository(s.db.DB()).UpdateEnabled(ctx, credential.ID, modelName, enabled)
	_ = s.syncSiteModelStatusFromAPIKeyModels(ctx, siteID, modelName, updatedAPIKeyModel)
	updated, err := updateCredentialMeta(ctx, credentialRepo, credential.ID, meta)
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	return s.workBuddyAccountSnapshot(ctx, updated), nil
}

func (s *Service) DeleteWorkBuddyAccount(ctx context.Context, siteID uuid.UUID, credentialID uuid.UUID) error {
	if err := s.requireWorkBuddySite(ctx, siteID); err != nil {
		return err
	}
	err := s.db.WithinTx(ctx, func(tx store.Tx) error {
		credential, err := store.NewSiteCredentialRepository(tx).GetByID(ctx, credentialID)
		if err != nil {
			return err
		}
		if credential.SiteID != siteID || !isWorkBuddyCredentialType(credential.CredentialType) {
			return fmt.Errorf("workbuddy account was not found")
		}
		// The relations are provider-agnostic: cooldowns, per-account models,
		// sync state, then the credential itself.
		return deleteGrokAccountRelations(ctx, tx, siteID, credentialID)
	})
	if err == nil {
		s.syncWorkBuddyQuotaSummary(ctx, siteID, store.Site{})
	}
	return err
}

// CompleteWorkBuddyLogin stores the tokens of a finished browser login. An
// existing account with the same identity (or the explicitly targeted one, for
// re-authorization) is updated in place instead of creating a duplicate.
func (s *Service) CompleteWorkBuddyLogin(ctx context.Context, siteID uuid.UUID, tokens oauthsvc.WorkBuddyTokens, targetCredentialID *uuid.UUID) (WorkBuddyAccount, error) {
	if err := s.requireWorkBuddySite(ctx, siteID); err != nil {
		return WorkBuddyAccount{}, err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return WorkBuddyAccount{}, fmt.Errorf("workbuddy tokens are missing an access token")
	}
	if strings.TrimSpace(tokens.Subject) == "" {
		tokens.Subject = oauthsvc.WorkBuddyTokenIdentity(tokens.AccessToken)
	}
	tokens.Realm = workBuddySpec().RegionOrDefault(tokens.Realm, tokens.Domain).ID
	defer s.siteLocks.lock(siteID)()
	secret, err := oauthsvc.EncodeWorkBuddyBundle(oauthsvc.WorkBuddyBundleFromTokens(tokens))
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	encrypted, _, err := s.credentials.Encrypt(secret)
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	var credential store.SiteCredential
	err = s.db.WithinTx(ctx, func(tx store.Tx) error {
		credentialRepo := store.NewSiteCredentialRepository(tx)
		credentials, err := credentialRepo.ListBySite(ctx, siteID)
		if err != nil {
			return err
		}
		existing, found, err := s.resolveWorkBuddyCredentialForLogin(credentials, tokens, siteID, targetCredentialID)
		if err != nil {
			return err
		}
		meta := map[string]any{}
		credentialType := workBuddyCredentialPrefix + uuid.NewString()
		if found {
			meta = grokMetaMap(existing.Meta)
			credentialType = existing.CredentialType
		}
		meta["enabled"] = true
		delete(meta, "reauth_required")
		if value := strings.TrimSpace(tokens.Email); value != "" {
			meta["email"] = value
		}
		if value := strings.TrimSpace(tokens.Subject); value != "" {
			meta["subject"] = value
		}
		if value := strings.TrimSpace(tokens.Domain); value != "" {
			meta["domain"] = value
		}
		meta["realm"] = tokens.Realm
		encodedMeta, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("marshal workbuddy account meta: %w", err)
		}
		credential, err = credentialRepo.Upsert(ctx, store.UpsertSiteCredentialParams{
			SiteID:          siteID,
			CredentialType:  credentialType,
			EncryptedSecret: encrypted,
			MaskedSecret:    workBuddyMaskedLabel(tokens),
			Meta:            encodedMeta,
		})
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if err := store.NewRouteCooldownRepository(tx).DeleteBySiteCredential(ctx, siteID, credential.ID); err != nil {
			return err
		}
		states, err := store.NewSiteAPIKeyStateRepository(tx).ListBySite(ctx, siteID)
		if err != nil {
			return err
		}
		for _, state := range states {
			if state.SiteCredentialID == credential.ID {
				_, err = store.NewSiteAPIKeyStateRepository(tx).RecoverPending(ctx, credential.ID)
				return err
			}
		}
		return nil
	})
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	return s.workBuddyAccountSnapshot(ctx, credential), nil
}

func (s *Service) resolveWorkBuddyCredentialForLogin(credentials []store.SiteCredential, tokens oauthsvc.WorkBuddyTokens, siteID uuid.UUID, targetCredentialID *uuid.UUID) (store.SiteCredential, bool, error) {
	if targetCredentialID != nil && *targetCredentialID != uuid.Nil {
		for _, credential := range credentials {
			if credential.ID == *targetCredentialID && credential.SiteID == siteID && isWorkBuddyCredentialType(credential.CredentialType) {
				return credential, true, nil
			}
		}
		return store.SiteCredential{}, false, fmt.Errorf("workbuddy account was not found")
	}
	wanted := strings.TrimSpace(tokens.Subject)
	if wanted == "" {
		return store.SiteCredential{}, false, nil
	}
	for _, credential := range credentials {
		if !isWorkBuddyCredentialType(credential.CredentialType) {
			continue
		}
		identity := strings.TrimSpace(anyString(grokMetaMap(credential.Meta)["subject"]))
		if identity == "" {
			if decrypted, err := s.credentials.Decrypt(credential.EncryptedSecret); err == nil {
				if bundle, decodeErr := oauthsvc.DecodeWorkBuddyBundle(decrypted); decodeErr == nil {
					identity = oauthsvc.WorkBuddyTokenIdentity(bundle.AccessToken)
				}
			}
		}
		if identity == wanted {
			return credential, true, nil
		}
	}
	return store.SiteCredential{}, false, nil
}

func workBuddyMaskedLabel(tokens oauthsvc.WorkBuddyTokens) string {
	if email := strings.TrimSpace(tokens.Email); email != "" {
		return email
	}
	token := strings.TrimSpace(tokens.AccessToken)
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "…" + token[len(token)-4:]
}

func (s *Service) workBuddyAccountSnapshot(ctx context.Context, credential store.SiteCredential) WorkBuddyAccount {
	state := grokAccountState(ctx, s.db.DB(), credential.ID)
	models, _ := store.NewSiteAPIKeyModelRepository(s.db.DB()).ListByCredential(ctx, credential.ID)
	account, _ := workBuddyAccountFromStore(credential, state, models)
	account.Models = s.resolveAccountModelEndpoints(ctx, account.Models)
	return account
}

// SetWorkBuddyAccountModelEndpoint adjusts the protocols one account may use
// for a model (the same override the API key model dialog edits).
func (s *Service) SetWorkBuddyAccountModelEndpoint(ctx context.Context, siteID uuid.UUID, credentialID uuid.UUID, siteModelID uuid.UUID, mode string, endpointTypes []string) (WorkBuddyAccount, error) {
	if err := s.requireWorkBuddySite(ctx, siteID); err != nil {
		return WorkBuddyAccount{}, err
	}
	credential, err := s.setAccountModelEndpointOverride(ctx, siteID, credentialID, siteModelID, mode, endpointTypes, isWorkBuddyCredentialType)
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	return s.workBuddyAccountSnapshot(ctx, credential), nil
}

func workBuddyAccountFromStore(credential store.SiteCredential, state store.SiteAPIKeyState, models []store.SiteAPIKeyModel) (WorkBuddyAccount, bool) {
	if !isWorkBuddyCredentialType(credential.CredentialType) {
		return WorkBuddyAccount{}, false
	}
	meta := grokMetaMap(credential.Meta)
	enabled := boolFromMeta(meta, "enabled", true)
	if state.SiteCredentialID != uuid.Nil && state.SiteCredentialID == credential.ID {
		enabled = state.Enabled
	}
	label := strings.TrimSpace(anyString(meta["email"]))
	if label == "" {
		label = credential.MaskedSecret
	}
	accountModels := make([]GrokAccountModel, 0, len(models))
	modelCount := 0
	for _, model := range models {
		if !model.Available {
			continue
		}
		modelCount++
		display := strings.TrimSpace(model.DisplayName)
		if display == "" {
			display = model.UpstreamModelName
		}
		accountModels = append(accountModels, newAccountModel(model, display))
	}
	account := WorkBuddyAccount{
		CredentialID: credential.ID,
		MaskedToken:  label,
		Enabled:      enabled,
		Region:       workBuddySpec().RegionOrDefault(anyString(meta["realm"]), anyString(meta["domain"])).ID,
		SyncStatus:   state.SyncStatus,
		ModelCount:   modelCount,
		Models:       accountModels,
	}
	if state.SyncMessage.Valid {
		account.SyncMessage = state.SyncMessage.String
	}
	if state.RemainQuota.Valid {
		value := state.RemainQuota.Int64
		account.RemainingQuota = &value
	}
	if state.UsedQuota.Valid {
		value := state.UsedQuota.Int64
		account.UsedQuota = &value
	}
	if len(state.Usage) > 0 {
		var usage struct {
			Limit    *int64                  `json:"limit"`
			Unit     string                  `json:"unit"`
			Packages []WorkBuddyQuotaPackage `json:"packages"`
		}
		if json.Unmarshal(state.Usage, &usage) == nil {
			account.QuotaLimit = usage.Limit
			account.QuotaUnit = usage.Unit
			account.QuotaPackages = usage.Packages
		}
	}
	return account, true
}

// ---------------------------------------------------------------------------
// refresh

func (s *Service) RefreshWorkBuddyAccount(ctx context.Context, siteID uuid.UUID, credentialID uuid.UUID) (WorkBuddyAccount, error) {
	defer s.siteLocks.lock(siteID)()
	if err := s.requireWorkBuddySite(ctx, siteID); err != nil {
		return WorkBuddyAccount{}, err
	}
	site, err := s.Get(ctx, siteID)
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	credentialRepo := store.NewSiteCredentialRepository(s.db.DB())
	credential, err := credentialRepo.GetByID(ctx, credentialID)
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	if credential.SiteID != siteID || !isWorkBuddyCredentialType(credential.CredentialType) {
		return WorkBuddyAccount{}, fmt.Errorf("workbuddy account was not found")
	}
	module, ok := s.adapters.ModuleForSiteType(adapter.WorkBuddySiteType)
	if !ok {
		return WorkBuddyAccount{}, fmt.Errorf("workbuddy adapter is not registered")
	}
	fetcher, ok := adapter.AsAPIKeySummaryFetcher(module)
	if !ok {
		return WorkBuddyAccount{}, fmt.Errorf("workbuddy adapter does not support account refresh")
	}
	access, err := s.oauth.EnsureWorkBuddyAccess(ctx, credential.ID)
	if err != nil {
		wrapped := fmt.Errorf("workbuddy account token: %w", err)
		s.recordWorkBuddyRefreshFailure(ctx, credential, wrapped)
		return WorkBuddyAccount{}, wrapped
	}
	summary, err := fetcher.SummarizeAPIKey(ctx, workBuddySiteConfig(s.toAdapterSite(ctx, site), access), access.Token)
	if err != nil {
		s.recordWorkBuddyRefreshFailure(ctx, credential, err)
		return WorkBuddyAccount{}, err
	}
	now := time.Now()
	// persistGrokRefresh only writes generic per-credential state (tier/email
	// meta, key state, per-account models), so it is shared here.
	state, models, err := persistGrokRefresh(ctx, s.db, credential, summary, now)
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	siteModels, err := s.syncSiteModelsFromAPIKeyState(ctx, siteID, site.SiteType, site.BaseURL)
	if err != nil {
		return WorkBuddyAccount{}, err
	}
	if err := s.updateWorkBuddySiteStateAfterRefresh(ctx, siteID, siteModels, now); err != nil {
		return WorkBuddyAccount{}, err
	}
	s.syncWorkBuddyQuotaSummary(ctx, siteID, store.Site{})
	updated, err := credentialRepo.GetByID(ctx, credentialID)
	if err != nil {
		updated = credential
	}
	account, _ := workBuddyAccountFromStore(updated, state, models)
	account.Models = s.resolveAccountModelEndpoints(ctx, account.Models)
	return account, nil
}

func (s *Service) RefreshWorkBuddyAccounts(ctx context.Context, siteID uuid.UUID) ([]WorkBuddyRefreshBatchItem, error) {
	accounts, err := s.ListWorkBuddyAccounts(ctx, siteID)
	if err != nil {
		return nil, err
	}
	results := make([]WorkBuddyRefreshBatchItem, 0, len(accounts))
	for _, account := range accounts {
		refreshed, refreshErr := s.RefreshWorkBuddyAccount(ctx, siteID, account.CredentialID)
		item := WorkBuddyRefreshBatchItem{CredentialID: account.CredentialID}
		if refreshErr != nil {
			item.Error = refreshErr.Error()
		} else {
			item.Account = &refreshed
		}
		results = append(results, item)
	}
	return results, nil
}

func (s *Service) updateWorkBuddySiteStateAfterRefresh(ctx context.Context, siteID uuid.UUID, siteModels []store.SiteModel, now time.Time) error {
	apiKeyCount, err := s.workBuddySiteAccountCount(ctx, siteID)
	if err != nil {
		return err
	}
	repo := store.NewSiteStateRepository(s.db.DB())
	existing, err := repo.GetBySite(ctx, siteID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	_, err = repo.Upsert(ctx, store.UpsertSiteStateParams{
		SiteID:            siteID,
		ValidationOK:      true,
		SyncStatus:        "synced",
		LastSyncStartedAt: existing.LastSyncStartedAt,
		LastSyncedAt:      now,
		APIKeyCount:       apiKeyCount,
		ModelCount:        len(availableSiteModels(siteModels)),
		RawStatus:         existing.RawStatus,
		UserSummary:       existing.UserSummary,
		Pricing:           existing.Pricing,
		Checkin:           existing.Checkin,
	})
	return err
}

func (s *Service) workBuddySiteAccountCount(ctx context.Context, siteID uuid.UUID) (int, error) {
	credentials, err := store.NewSiteCredentialRepository(s.db.DB()).ListBySite(ctx, siteID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, credential := range credentials {
		if isWorkBuddyCredentialType(credential.CredentialType) {
			count++
		}
	}
	return count, nil
}

func (s *Service) recordWorkBuddyRefreshFailure(ctx context.Context, credential store.SiteCredential, refreshErr error) {
	now := time.Now()
	meta := grokMetaMap(credential.Meta)
	enabled := boolFromMeta(meta, "enabled", true)
	reauth := shouldReauthorizeWorkBuddyCredential(refreshErr)
	if reauth {
		enabled = false
		meta["enabled"] = false
		meta["reauth_required"] = true
		_, _ = store.NewSiteCredentialRepository(s.db.DB()).UpdateMeta(ctx, credential.ID, store.JSON(jsonBytes(meta)))
	}
	state, err := store.NewSiteAPIKeyStateRepository(s.db.DB()).UpsertSyncResult(ctx, store.UpsertSiteAPIKeyStateParams{
		SiteCredentialID: credential.ID,
		SiteID:           credential.SiteID,
		Enabled:          enabled,
		SyncStatus:       workBuddyRefreshFailureStatus(refreshErr),
		SyncMessage:      refreshErr.Error(),
		LastSyncedAt:     now,
	})
	if err == nil && !enabled && state.Enabled {
		_, _ = store.NewSiteAPIKeyStateRepository(s.db.DB()).UpdateEnabled(ctx, credential.ID, false)
	}
}

func workBuddyRefreshFailureStatus(err error) string {
	if shouldReauthorizeWorkBuddyCredential(err) {
		return "reauth_required"
	}
	return "stale"
}

func shouldReauthorizeWorkBuddyCredential(err error) bool {
	return adapter.IsWorkBuddyUnauthorizedError(err) || errors.Is(err, oauthsvc.ErrWorkBuddyReauthorizationRequired)
}

// ---------------------------------------------------------------------------
// quota

// applyWorkBuddySummaryToState copies the account summary of a refresh (tier,
// quota, raw payload) into the key-state columns so a site refresh keeps the
// same data as an account refresh.
func applyWorkBuddySummaryToState(params *store.UpsertSiteAPIKeyStateParams, summary adapter.APIKeySummary, name string) {
	tier, _, remaining, used, unlimited := grokRefreshQuotaValues(summary.Usage)
	params.Name = name
	params.GroupName = tier
	params.RemainQuota = remaining
	params.UsedQuota = used
	params.UnlimitedQuota = unlimited
	params.Usage = store.JSON(jsonBytes(summary.Usage))
	params.Raw = store.JSON(jsonBytes(summary.Raw))
}

// keepWorkBuddyStateQuota carries the last known quota over a failed refresh.
func keepWorkBuddyStateQuota(params *store.UpsertSiteAPIKeyStateParams, previous store.SiteAPIKeyState) {
	params.Name = previous.Name
	if previous.GroupName.Valid {
		params.GroupName = previous.GroupName.String
	}
	if previous.RemainQuota.Valid {
		params.RemainQuota = previous.RemainQuota.Int64
	}
	if previous.UsedQuota.Valid {
		params.UsedQuota = previous.UsedQuota.Int64
	}
	params.UnlimitedQuota = previous.UnlimitedQuota
	params.Usage = previous.Usage
	params.Raw = previous.Raw
}

// syncWorkBuddyQuotaSummary writes the site-level quota summary the sites table
// shows (meta.quota_probe_summary): remaining/limit credits summed over the
// enabled accounts. It returns the updated site, or the given/loaded one when
// nothing changed. current may be the zero value to have the site loaded.
func (s *Service) syncWorkBuddyQuotaSummary(ctx context.Context, siteID uuid.UUID, current store.Site) store.Site {
	siteRepo := store.NewSiteRepository(s.db.DB())
	item := current
	if item.ID == uuid.Nil {
		loaded, err := siteRepo.GetByID(ctx, siteID)
		if err != nil {
			return current
		}
		item = loaded
	}
	credentials, err := store.NewSiteCredentialRepository(s.db.DB()).ListBySite(ctx, siteID)
	if err != nil {
		return item
	}
	states, err := store.NewSiteAPIKeyStateRepository(s.db.DB()).ListBySite(ctx, siteID)
	if err != nil {
		return item
	}
	statesByCredential := make(map[uuid.UUID]store.SiteAPIKeyState, len(states))
	for _, state := range states {
		statesByCredential[state.SiteCredentialID] = state
	}
	var accounts, withQuota int
	var remaining, limit, used float64
	plan := ""
	for _, credential := range credentials {
		if !isWorkBuddyCredentialType(credential.CredentialType) {
			continue
		}
		accounts++
		state := statesByCredential[credential.ID]
		if !state.Enabled || !state.RemainQuota.Valid {
			continue
		}
		var usage struct {
			Limit *float64 `json:"limit"`
			Tier  string   `json:"tier"`
		}
		_ = json.Unmarshal(state.Usage, &usage)
		withQuota++
		remaining += float64(state.RemainQuota.Int64)
		if usage.Limit != nil {
			limit += *usage.Limit
		} else if state.UsedQuota.Valid {
			limit += float64(state.RemainQuota.Int64 + state.UsedQuota.Int64)
		}
		if state.UsedQuota.Valid {
			used += float64(state.UsedQuota.Int64)
		}
		if plan == "" {
			plan = usage.Tier
		}
	}
	meta := siteMetaMap(item)
	if accounts == 0 || withQuota == 0 {
		if _, ok := meta[siteMetaQuotaProbeSummary]; !ok {
			return item
		}
		delete(meta, siteMetaQuotaProbeSummary)
	} else {
		summary := map[string]any{
			"probe_type":       "workbuddy",
			"status":           "ok",
			"remaining_min":    remaining,
			"limit":            limit,
			"used":             used,
			"unit":             "credits",
			"credential_count": accounts,
			"ok_count":         withQuota,
			"fetched_at":       time.Now().UTC().Format(time.RFC3339),
		}
		if plan != "" {
			summary["plan"] = plan
		}
		meta[siteMetaQuotaProbeSummary] = summary
	}
	updated, err := siteRepo.UpdateMeta(ctx, item.ID, store.JSON(jsonBytes(meta)))
	if err != nil {
		return item
	}
	return updated
}
