import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ExternalLink, KeyRound, LoaderCircle, RotateCcw, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  AccountHeaderActions,
  AccountList,
  AccountModelsDialog,
  AccountRow,
  AccountsListDialog,
} from '@/features/sites/components/account-dialogs'
import { useNestedDialogClassNames } from '@/features/sites/components/use-nested-dialog'
import { WorkBuddyImportDialog } from '@/features/sites/components/workbuddy-import-dialog'
import { cn } from '@/lib/utils'
import { toast } from '@/lib/toast'
import {
  deleteWorkBuddyAccount,
  listWorkBuddyAccounts,
  listWorkBuddyRegions,
  pollWorkBuddyLogin,
  refreshWorkBuddyAccount,
  refreshWorkBuddyAccounts,
  sitesQueryKeys,
  startWorkBuddyLogin,
  updateWorkBuddyAccount,
  updateWorkBuddyAccountModel,
  type AccountModelUpdate,
  type Site,
  type WorkBuddyAccount,
  type WorkBuddyLoginStart,
  type WorkBuddyRegion,
  type WorkBuddyRegionsResponse,
} from '@/features/sites/api/sites'

const EMPTY_ACCOUNTS: WorkBuddyAccount[] = []
const EMPTY_REGIONS: WorkBuddyRegion[] = []

type LoginState = {
  status: 'starting' | 'waiting' | 'error'
  purpose: 'add' | 'reauth'
  credentialId?: string
  info?: WorkBuddyLoginStart
  error?: string
}

/**
 * WorkBuddy account manager. Same shape as the Grok one, but the login is a
 * plain browser login: we open `auth_url` and poll with the returned `state`
 * (there is no user code to type in).
 */
export function WorkBuddyAccountsDraw({
  site,
  onOpenChange,
}: {
  site: Site | null
  onOpenChange: (open: boolean) => void
}) {
  const { t, i18n } = useTranslation('sites')
  const queryClient = useQueryClient()
  const [deleting, setDeleting] = useState<WorkBuddyAccount | null>(null)
  const [login, setLogin] = useState<LoginState | null>(null)
  const [modelsAccount, setModelsAccount] = useState<WorkBuddyAccount | null>(null)
  const [importOpen, setImportOpen] = useState(false)
  // Choosing the site (China / international) to log in to, before the login starts.
  const [regionPickOpen, setRegionPickOpen] = useState(false)
  const [pickedRegion, setPickedRegion] = useState('')
  const open = Boolean(site)
  const nestedZ = useNestedDialogClassNames()
  const nestedOpen = Boolean(login) || Boolean(deleting) || Boolean(modelsAccount) || importOpen || regionPickOpen
  const queryKey = site
    ? [...sitesQueryKeys.detail(site.id), 'workbuddy-accounts']
    : [...sitesQueryKeys.all, 'workbuddy-accounts', 'none']

  const accountsQuery = useQuery({
    queryKey,
    queryFn: () => site ? listWorkBuddyAccounts(site.id) : Promise.resolve({ items: EMPTY_ACCOUNTS }),
    enabled: open,
  })

  const regionsQuery = useQuery({
    queryKey: [...queryKey, 'regions'],
    queryFn: (): Promise<WorkBuddyRegionsResponse> => site ? listWorkBuddyRegions(site.id) : Promise.resolve({ items: [] as WorkBuddyRegion[] }),
    enabled: open,
    staleTime: Infinity,
  })
  const regions = regionsQuery.data?.items ?? EMPTY_REGIONS
  const defaultRegion = regionsQuery.data?.meta?.default ?? regions[0]?.id ?? ''

  function regionLabel(id: string) {
    const region = regions.find((item) => item.id === id)
    const fallback = region ? (i18n.language.startsWith('en') ? region.label_en : region.label) : id
    return t(`workbuddyAccounts.region.${id}`, { defaultValue: fallback })
  }

  const updateMutation = useMutation({
    mutationFn: ({ account, enabled }: { account: WorkBuddyAccount; enabled: boolean }) => {
      if (!site) throw new Error('site required')
      return updateWorkBuddyAccount(site.id, account.credential_id, { enabled })
    },
    onSuccess: async () => {
      await refreshAccountQueries()
      toast.success(t('workbuddyAccounts.toast.updated'))
    },
    onError: (error) => toast.error(t('workbuddyAccounts.toast.updateFailed'), { description: error.message }),
  })

  const deleteMutation = useMutation({
    mutationFn: (account: WorkBuddyAccount) => {
      if (!site) throw new Error('site required')
      return deleteWorkBuddyAccount(site.id, account.credential_id)
    },
    onSuccess: async () => {
      setDeleting(null)
      await refreshAccountQueries()
      toast.success(t('workbuddyAccounts.toast.deleted'))
    },
    onError: (error) => toast.error(t('workbuddyAccounts.toast.deleteFailed'), { description: error.message }),
  })

  const refreshMutation = useMutation({
    mutationFn: (account: WorkBuddyAccount) => {
      if (!site) throw new Error('site required')
      return refreshWorkBuddyAccount(site.id, account.credential_id)
    },
    onSuccess: async (result) => {
      await refreshAccountQueries()
      if (result.account.sync_message) {
        toast.warning(t('workbuddyAccounts.toast.refreshFailed'), { description: result.account.sync_message })
      } else {
        toast.success(t('workbuddyAccounts.toast.refreshed'))
      }
    },
    onError: (error) => toast.error(t('workbuddyAccounts.toast.refreshFailed'), { description: error.message }),
  })

  const refreshAllMutation = useMutation({
    mutationFn: () => {
      if (!site) throw new Error('site required')
      return refreshWorkBuddyAccounts(site.id)
    },
    onSuccess: async (result) => {
      await refreshAccountQueries()
      const failed = result.meta?.failed ?? 0
      if (failed > 0) {
        toast.warning(t('workbuddyAccounts.toast.refreshAllPartial'), {
          description: t('workbuddyAccounts.toast.refreshAllPartialDesc', { count: failed }),
        })
      } else {
        toast.success(t('workbuddyAccounts.toast.refreshedAll'))
      }
    },
    onError: (error) => toast.error(t('workbuddyAccounts.toast.refreshAllFailed'), { description: error.message }),
  })

  const updateModelMutation = useMutation({
    mutationFn: ({ credentialId, update }: { credentialId: string; update: AccountModelUpdate }) => {
      if (!site) throw new Error('site required')
      return updateWorkBuddyAccountModel(site.id, credentialId, update)
    },
    onSuccess: async (result) => {
      setModelsAccount((current) => (current?.credential_id === result.account.credential_id ? result.account : current))
      await refreshAccountQueries()
    },
    onError: (error) => toast.error(t('workbuddyAccounts.toast.updateFailed'), { description: error.message }),
  })

  const bulkModelMutation = useMutation({
    mutationFn: async ({ credentialId, models, enabled }: { credentialId: string; models: string[]; enabled: boolean }) => {
      if (!site) throw new Error('site required')
      let latest: Awaited<ReturnType<typeof updateWorkBuddyAccountModel>> | null = null
      let firstError: Error | null = null
      for (const model of models) {
        try {
          latest = await updateWorkBuddyAccountModel(site.id, credentialId, { model, enabled })
        } catch (error) {
          firstError ??= error instanceof Error ? error : new Error(String(error))
        }
      }
      if (!latest) throw firstError ?? new Error('no models to update')
      return { latest, firstError }
    },
    onSuccess: async ({ latest, firstError }) => {
      setModelsAccount((current) => (current?.credential_id === latest.account.credential_id ? latest.account : current))
      await refreshAccountQueries()
      if (firstError) toast.error(t('workbuddyAccounts.toast.updateFailed'), { description: firstError.message })
    },
    onError: (error) => toast.error(t('workbuddyAccounts.toast.updateFailed'), { description: error.message }),
  })

  async function refreshAccountQueries() {
    await queryClient.invalidateQueries({ queryKey })
    await queryClient.invalidateQueries({ queryKey: sitesQueryKeys.list() })
    if (site) await queryClient.invalidateQueries({ queryKey: sitesQueryKeys.models(site.id) })
  }

  async function startLogin(purpose: LoginState['purpose'], credentialId?: string, region?: string) {
    if (!site) return
    setLogin({ status: 'starting', purpose, credentialId })
    try {
      const result = await startWorkBuddyLogin(site.id, region)
      setLogin({ status: 'waiting', purpose, credentialId, info: result.login })
      window.open(result.login.auth_url, '_blank', 'noopener')
    } catch (error) {
      setLogin({ status: 'error', purpose, credentialId, error: (error as Error).message })
    }
  }

  useEffect(() => {
    if (!site || !login || login.status !== 'waiting' || !login.info) return
    let cancelled = false
    const state = login.info.state
    const intervalMs = Math.max(2, login.info.interval || 3) * 1000
    const timer = setInterval(async () => {
      try {
        const result = await pollWorkBuddyLogin(site.id, state, login.credentialId)
        if (cancelled) return
        if (result.login.status === 'complete') {
          setLogin(null)
          await refreshAccountQueries()
          toast.success(t('workbuddyAccounts.toast.created'))
        }
      } catch (error) {
        if (cancelled) return
        setLogin((current) => current ? { ...current, status: 'error', error: (error as Error).message } : null)
      }
    }, intervalMs)
    return () => {
      cancelled = true
      clearInterval(timer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [site?.id, login?.status, login?.info?.state])

  const items = accountsQuery.data?.items ?? EMPTY_ACCOUNTS
  const activeModelsAccount = modelsAccount
    ? (items.find((account) => account.credential_id === modelsAccount.credential_id) ?? modelsAccount)
    : null

  return (
    <>
      <AccountsListDialog
        open={open}
        title={t('workbuddyAccounts.title')}
        nestedOpen={nestedOpen}
        onCloseInnermost={() => {
          if (importOpen) setImportOpen(false)
          else if (regionPickOpen) setRegionPickOpen(false)
          else if (login) setLogin(null)
          else if (deleting) setDeleting(null)
          else if (modelsAccount) setModelsAccount(null)
        }}
        onOpenChange={(next) => {
          if (!next) {
            setDeleting(null)
            setLogin(null)
            setModelsAccount(null)
            setImportOpen(false)
            setRegionPickOpen(false)
          }
          onOpenChange(next)
        }}
        headerActions={(
          <AccountHeaderActions
            addLabel={t('workbuddyAccounts.add')}
            addDisabled={Boolean(login)}
            onAdd={() => {
              setPickedRegion((current) => current || defaultRegion)
              setRegionPickOpen(true)
            }}
            importLabel={t('workbuddyAccounts.import')}
            importDisabled={Boolean(login)}
            onImport={() => setImportOpen(true)}
            refreshAllLabel={t('workbuddyAccounts.refreshAll')}
            refreshingAll={refreshAllMutation.isPending}
            onRefreshAll={() => refreshAllMutation.mutate()}
          />
        )}
      >
        {accountsQuery.isLoading ? (
          <p className="py-10 text-center text-body text-label-2">{t('workbuddyAccounts.loading')}</p>
        ) : accountsQuery.isError ? (
          <p className="py-10 text-center text-body text-danger">{accountsQuery.error.message}</p>
        ) : items.length ? (
          <AccountList>
            {items.map((account) => {
              const refreshing = refreshMutation.isPending && refreshMutation.variables?.credential_id === account.credential_id
              const syncBadge = workBuddySyncBadge(account.sync_status)
              const reauthRequired = account.sync_status?.toLowerCase() === 'reauth_required'
              const name = account.masked_token || t('workbuddyAccounts.unnamed')
              return (
                <AccountRow
                  key={account.credential_id}
                  name={name}
                  badges={<Badge variant="secondary" className={`shrink-0 px-1.5 py-0 text-[10px] ${syncBadge.className}`}>{syncBadge.label(t)}</Badge>}
                  enabled={account.enabled}
                  toggleDisabled={updateMutation.isPending || reauthRequired}
                  toggleLabel={t('workbuddyAccounts.toggleLabel', { name })}
                  onToggle={(enabled) => updateMutation.mutate({ account, enabled })}
                  pills={account.region ? <Badge variant="neutral">{regionLabel(account.region)}</Badge> : undefined}
                  detail={<WorkBuddyQuota account={account} />}
                  message={account.sync_message || undefined}
                  modelCountLabel={`${account.model_count ?? account.models?.length ?? 0} ${t('accountModels.available')}`}
                  onOpenModels={() => setModelsAccount(account)}
                  actions={(
                    <>
                      {reauthRequired ? (
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-7 w-7 text-danger"
                          disabled={Boolean(login)}
                          title={t('workbuddyAccounts.reauthorize')}
                          aria-label={t('workbuddyAccounts.reauthorize')}
                          onClick={() => startLogin('reauth', account.credential_id, account.region)}
                        >
                          <KeyRound className="h-3.5 w-3.5" />
                        </Button>
                      ) : null}
                      <Button
                        size="icon"
                        variant="ghost"
                        className="h-7 w-7"
                        disabled={refreshing}
                        title={t('workbuddyAccounts.refresh')}
                        aria-label={t('workbuddyAccounts.refresh')}
                        onClick={() => refreshMutation.mutate(account)}
                      >
                        {refreshing ? <LoaderCircle className="h-3.5 w-3.5 animate-spin" /> : <RotateCcw className="h-3.5 w-3.5" />}
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        className="h-7 w-7 text-danger hover:bg-danger/10 hover:text-danger"
                        disabled={deleteMutation.isPending}
                        title={t('workbuddyAccounts.delete')}
                        aria-label={t('workbuddyAccounts.delete')}
                        onClick={() => setDeleting(account)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </>
                  )}
                />
              )
            })}
          </AccountList>
        ) : (
          <p className="py-10 text-center text-body text-label-2">{t('workbuddyAccounts.empty')}</p>
        )}
      </AccountsListDialog>

      <Dialog open={regionPickOpen} onOpenChange={setRegionPickOpen}>
        <DialogContent presentation="center" size="sm" overlayClassName={nestedZ.overlayClassName} className={nestedZ.className}>
          <DialogHeader className="border-b-0 pb-2"><DialogTitle>{t('workbuddyAccounts.regionDialog.title')}</DialogTitle></DialogHeader>
          <DialogBody className="space-y-3 pt-0">
            <DialogDescription className="mt-0">{t('workbuddyAccounts.regionDialog.description')}</DialogDescription>
            {regionsQuery.isLoading ? (
              <p className="flex items-center gap-2 text-body text-label-2"><LoaderCircle className="h-4 w-4 animate-spin" />{t('workbuddyAccounts.loading')}</p>
            ) : regionsQuery.isError ? (
              <p className="text-body text-danger">{regionsQuery.error.message}</p>
            ) : (
              <div role="radiogroup" className="space-y-2">
                {regions.map((region) => {
                  const selected = (pickedRegion || defaultRegion) === region.id
                  const hint = t(`workbuddyAccounts.regionDialog.${region.id}Hint`, { defaultValue: '' })
                  return (
                    <button
                      key={region.id}
                      type="button"
                      role="radio"
                      aria-checked={selected}
                      onClick={() => setPickedRegion(region.id)}
                      className={cn(
                        'w-full rounded-inner border px-3 py-2.5 text-left transition-colors',
                        selected ? 'border-tint bg-tint/5' : 'border-separator bg-elevated-1 hover:border-tint/50 hover:bg-fill-3',
                      )}
                    >
                      <span className="block text-body font-medium text-label">{regionLabel(region.id)}</span>
                      {hint ? <span className="block text-footnote text-label-2">{hint}</span> : null}
                    </button>
                  )
                })}
              </div>
            )}
          </DialogBody>
          <DialogFooter
            className="border-t-0 pt-2"
            cancel={<Button variant="secondary" onClick={() => setRegionPickOpen(false)}>{t('workbuddyAccounts.cancel')}</Button>}
            confirm={(
              <Button
                disabled={!regions.length || Boolean(login)}
                onClick={() => {
                  const region = pickedRegion || defaultRegion
                  setRegionPickOpen(false)
                  void startLogin('add', undefined, region)
                }}
              >
                {t('workbuddyAccounts.regionDialog.continue')}
              </Button>
            )}
          />
        </DialogContent>
      </Dialog>

      {site ? (
        <WorkBuddyImportDialog
          open={importOpen}
          siteId={site.id}
          regionLabel={regionLabel}
          onOpenChange={setImportOpen}
          onImported={refreshAccountQueries}
        />
      ) : null}

      <Dialog open={Boolean(login)} onOpenChange={(next) => { if (!next) setLogin(null) }}>
        <DialogContent presentation="center" size="sm" overlayClassName={nestedZ.overlayClassName} className={nestedZ.className}>
          <DialogHeader className="border-b-0 pb-2"><DialogTitle>{t(login?.purpose === 'reauth' ? 'workbuddyAccounts.login.reauthTitle' : 'workbuddyAccounts.login.title')}</DialogTitle></DialogHeader>
          <DialogBody className="space-y-4 pt-0">
            {login?.status === 'starting' ? (
              <p className="flex items-center gap-2 text-body text-label-2"><LoaderCircle className="h-4 w-4 animate-spin" />{t('workbuddyAccounts.login.starting')}</p>
            ) : login?.status === 'error' ? (
              <p className="text-body text-danger">{login.error}</p>
            ) : login?.info ? (
              <>
                <DialogDescription className="mt-0">{t('workbuddyAccounts.login.instructions')}</DialogDescription>
                <Button variant="secondary" className="w-full" onClick={() => window.open(login.info!.auth_url, '_blank', 'noopener')}>
                  <ExternalLink className="h-4 w-4" />{t('workbuddyAccounts.login.authorize')}
                </Button>
                <p className="flex items-center justify-center gap-2 text-footnote text-label-2"><LoaderCircle className="h-3.5 w-3.5 animate-spin" />{t('workbuddyAccounts.login.waiting')}</p>
              </>
            ) : null}
          </DialogBody>
          <DialogFooter className="border-t-0 pt-2">
            <Button variant="secondary" onClick={() => setLogin(null)}>{t('workbuddyAccounts.cancel')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(deleting)} onOpenChange={(next) => { if (!next && !deleteMutation.isPending) setDeleting(null) }}>
        <DialogContent presentation="center" size="sm" overlayClassName={nestedZ.overlayClassName} className={nestedZ.className}>
          <DialogHeader className="border-b-0 pb-2"><DialogTitle>{t('workbuddyAccounts.deleteDialog.title')}</DialogTitle></DialogHeader>
          <DialogBody className="pt-0">
            <DialogDescription className="mt-0">{t('workbuddyAccounts.deleteDialog.confirm', { name: deleting?.masked_token || t('workbuddyAccounts.unnamed') })}</DialogDescription>
          </DialogBody>
          <DialogFooter
            className="border-t-0 pt-2"
            cancel={<Button variant="secondary" disabled={deleteMutation.isPending} onClick={() => setDeleting(null)}>{t('workbuddyAccounts.cancel')}</Button>}
            confirm={(
              <Button variant="destructive" disabled={deleteMutation.isPending} onClick={() => { if (deleting) deleteMutation.mutate(deleting) }}>
                {deleteMutation.isPending ? <LoaderCircle className="h-4 w-4 animate-spin" /> : null}{t('workbuddyAccounts.deleteDialog.delete')}
              </Button>
            )}
          />
        </DialogContent>
      </Dialog>

      <AccountModelsDialog
        key={activeModelsAccount?.credential_id ?? 'workbuddy-account-models'}
        open={Boolean(activeModelsAccount)}
        title={activeModelsAccount ? `${activeModelsAccount.masked_token} ${t('accountModels.available')}` : t('accountModels.available')}
        models={activeModelsAccount?.models ?? []}
        backLabel={t('accountModels.backToAccounts')}
        pendingModel={updateModelMutation.isPending ? updateModelMutation.variables?.update.model : undefined}
        bulkPending={bulkModelMutation.isPending}
        protocolPending={updateModelMutation.isPending && Boolean(updateModelMutation.variables?.update.endpointMode)}
        onBack={() => setModelsAccount(null)}
        onClose={() => setModelsAccount(null)}
        onToggleModel={(model, enabled) => {
          if (!activeModelsAccount) return
          updateModelMutation.mutate({ credentialId: activeModelsAccount.credential_id, update: { model: model.upstream_name, enabled } })
        }}
        onBulkToggle={(models, enabled) => {
          if (!activeModelsAccount) return
          bulkModelMutation.mutate({ credentialId: activeModelsAccount.credential_id, models, enabled })
        }}
        onSaveProtocol={(update) => {
          if (!activeModelsAccount) return
          updateModelMutation.mutate({ credentialId: activeModelsAccount.credential_id, update })
        }}
      />
    </>
  )
}

const creditFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 })

function formatCredits(value: number) {
  return creditFormatter.format(value)
}

function formatQuotaDate(value?: string) {
  if (!value) return ''
  const date = new Date(value.includes('T') ? value : value.replace(' ', 'T') + '+08:00')
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString()
}

function WorkBuddyQuota({ account }: { account: WorkBuddyAccount }) {
  const { t } = useTranslation('sites')
  const limit = account.quota_limit
  const remaining = account.remaining_quota
  if (limit == null || remaining == null) {
    return <p className="text-footnote text-label-2">{t('workbuddyAccounts.quota.unknown')}</p>
  }
  const ratio = limit > 0 ? Math.min(Math.max(remaining / limit, 0), 1) : 0
  const low = ratio < 0.1
  return (
    <div className="space-y-2">
      <div className="space-y-1">
        <div className="flex items-baseline justify-between text-footnote">
          <span className="text-label-2">{t('workbuddyAccounts.quota.credits')}</span>
          <span className={`tabular-nums font-medium ${low ? 'text-danger' : 'text-label'}`}>
            {formatCredits(remaining)} / {formatCredits(limit)}
          </span>
        </div>
        <div className="h-1.5 overflow-hidden rounded-full bg-fill-1">
          <div className={`h-full rounded-full ${low ? 'bg-danger' : 'bg-success'}`} style={{ width: `${ratio * 100}%` }} />
        </div>
      </div>
      {account.quota_packages?.length ? (
        <ul className="space-y-0.5 text-footnote text-label-2">
          {account.quota_packages.map((pkg) => (
            <li key={`${pkg.name}:${pkg.cycle_end ?? ''}`} className="flex items-baseline justify-between gap-3">
              <span className="min-w-0 truncate">{pkg.name}</span>
              <span className="shrink-0 tabular-nums">
                {formatCredits(pkg.remaining)} / {formatCredits(pkg.size)}
                {' · '}
                {pkg.recurring
                  ? t('workbuddyAccounts.quota.resetsOn', { date: formatQuotaDate(pkg.cycle_end) })
                  : t('workbuddyAccounts.quota.expiresOn', { date: formatQuotaDate(pkg.expires_at || pkg.cycle_end) })}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}

function workBuddySyncBadge(status?: string | null): { className: string; label: (t: (key: string) => string) => string } {
  switch ((status || '').toLowerCase()) {
    case 'synced':
      return { className: 'border-success/40 text-success', label: (t) => t('workbuddyAccounts.sync.synced') }
    case 'failed':
      return { className: 'border-danger/40 text-danger', label: (t) => t('workbuddyAccounts.sync.failed') }
    case 'reauth_required':
      return { className: 'border-danger/40 text-danger', label: (t) => t('workbuddyAccounts.sync.reauthRequired') }
    case 'stale':
      return { className: 'border-warning/40 text-warning', label: (t) => t('workbuddyAccounts.sync.stale') }
    case 'partial':
      return { className: 'border-warning/40 text-warning', label: (t) => t('workbuddyAccounts.sync.partial') }
    default:
      return { className: 'text-label-2', label: (t) => t('workbuddyAccounts.sync.unknown') }
  }
}
