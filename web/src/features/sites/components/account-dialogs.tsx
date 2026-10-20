import { useState, type ReactNode } from 'react'
import { LoaderCircle, RotateCcw, Settings2, Upload } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ModelsDraw, type ModelsDrawItem } from '@/components/common/models-draw'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { formatEndpointTypeLabel } from '@/features/models/lib/model-helpers'
import { modelNameIconInfo } from '@/features/sites/lib/model-icon'
import type { AccountModel } from '@/features/sites/api/sites'
import { useMobileLayout } from '@/hooks/use-media-query'

/**
 * Shared dialogs for OAuth account sites (Grok, WorkBuddy). They follow the
 * same presentation as the API key manager of other sites: a centered dialog on
 * desktop (a sheet on mobile) for the list, a nested dialog for the models and
 * a nested dialog for the per-model protocols.
 */

export function AccountsListDialog({
  open,
  title,
  headerActions,
  nestedOpen,
  onOpenChange,
  onCloseInnermost,
  children,
}: {
  open: boolean
  title: string
  headerActions?: ReactNode
  /** True while any nested dialog (login, delete, models) is open. */
  nestedOpen: boolean
  onOpenChange: (open: boolean) => void
  /** Closes the innermost nested dialog; called instead of closing the list. */
  onCloseInnermost: () => void
  children: ReactNode
}) {
  const isMobile = useMobileLayout()

  function handleOpenChange(next: boolean) {
    if (!next && !isMobile && nestedOpen) {
      onCloseInnermost()
      return
    }
    onOpenChange(next)
  }

  if (isMobile) {
    return (
      <Sheet open={open} onOpenChange={handleOpenChange}>
        <SheetContent>
          <SheetHeader className="flex items-center justify-between gap-3">
            <SheetTitle>{title}</SheetTitle>
            {headerActions}
          </SheetHeader>
          <SheetBody>{children}</SheetBody>
        </SheetContent>
      </Sheet>
    )
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        size="md"
        onOpenAutoFocus={(event) => event.preventDefault()}
        onPointerDownOutside={(event) => {
          if (nestedOpen) event.preventDefault()
        }}
        onInteractOutside={(event) => {
          if (nestedOpen) event.preventDefault()
        }}
        onEscapeKeyDown={(event) => {
          if (!nestedOpen) return
          event.preventDefault()
          handleOpenChange(false)
        }}
      >
        <DialogHeader className="flex flex-row items-center justify-between gap-3">
          <DialogTitle>{title}</DialogTitle>
          {headerActions}
        </DialogHeader>
        <DialogBody className="min-h-0 flex-1 overflow-y-auto">{children}</DialogBody>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Header actions: the primary "add" button, an optional "import" button on its
 * left, plus a quiet refresh-all icon.
 */
export function AccountHeaderActions({
  addLabel,
  addDisabled,
  onAdd,
  importLabel,
  importDisabled,
  onImport,
  refreshAllLabel,
  refreshingAll,
  onRefreshAll,
}: {
  addLabel: string
  addDisabled?: boolean
  onAdd: () => void
  importLabel?: string
  importDisabled?: boolean
  onImport?: () => void
  refreshAllLabel: string
  refreshingAll?: boolean
  onRefreshAll: () => void
}) {
  return (
    <div className="flex items-center gap-2">
      <Button
        size="icon"
        variant="ghost"
        className="h-8 w-8"
        title={refreshAllLabel}
        aria-label={refreshAllLabel}
        disabled={refreshingAll}
        onClick={onRefreshAll}
      >
        {refreshingAll ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <RotateCcw className="h-4 w-4" />}
      </Button>
      {onImport && importLabel ? (
        <Button size="sm" variant="secondary" disabled={importDisabled} onClick={onImport}>
          <Upload className="h-3.5 w-3.5" />
          {importLabel}
        </Button>
      ) : null}
      <Button size="sm" disabled={addDisabled} onClick={onAdd}>
        {addLabel}
      </Button>
    </div>
  )
}

/** Same list container as the API key manager: divided rows on desktop, cards on mobile. */
export function AccountList({ children }: { children: ReactNode }) {
  const isMobile = useMobileLayout()
  return <div className={isMobile ? 'space-y-3' : 'divide-y divide-separator'}>{children}</div>
}

/**
 * One account row, laid out like an API key row: name + status + switch, a line
 * of pills, an inset detail box, then the summary / model count on the left and
 * the icon actions on the right.
 */
export function AccountRow({
  name,
  badges,
  enabled,
  toggleDisabled,
  toggleLabel,
  onToggle,
  pills,
  detail,
  message,
  summary,
  modelCountLabel,
  onOpenModels,
  actions,
}: {
  name: string
  badges?: ReactNode
  enabled: boolean
  toggleDisabled?: boolean
  toggleLabel: string
  onToggle: (enabled: boolean) => void
  pills?: ReactNode
  detail?: ReactNode
  message?: string
  summary?: ReactNode
  modelCountLabel: string
  onOpenModels: () => void
  actions: ReactNode
}) {
  const isMobile = useMobileLayout()
  return (
    <div className={isMobile ? 'mat-regular space-y-3 rounded-card p-4' : 'space-y-3 py-4 first:pt-0 last:pb-0'}>
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate text-body font-medium text-label">{name}</span>
          {badges}
        </div>
        <Switch checked={enabled} disabled={toggleDisabled} aria-label={toggleLabel} onCheckedChange={onToggle} />
      </div>
      {pills ? <div className="flex flex-wrap items-center gap-2 text-footnote text-label-2">{pills}</div> : null}
      {detail ? <div className="rounded-control bg-fill-3 px-3 py-2">{detail}</div> : null}
      {message ? <p className="text-footnote text-danger">{message}</p> : null}
      <div className="flex items-center justify-between text-footnote text-label-2">
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          {summary}
          <button type="button" className="cursor-pointer hover:text-label" onClick={onOpenModels}>
            {modelCountLabel}
          </button>
        </div>
        <div className="flex items-center gap-1">{actions}</div>
      </div>
    </div>
  )
}

type ProtocolMode = 'inherit' | 'allowlist' | 'disabled'

export type AccountModelProtocolUpdate = {
  model: string
  enabled: boolean
  siteModelId: string
  endpointMode: ProtocolMode
  endpointTypes: string[]
}

export function AccountModelsDialog({
  open,
  title,
  models,
  backLabel,
  pendingModel,
  bulkPending,
  protocolPending,
  onBack,
  onClose,
  onToggleModel,
  onBulkToggle,
  onSaveProtocol,
}: {
  open: boolean
  title: string
  models: AccountModel[]
  backLabel: string
  pendingModel?: string
  bulkPending?: boolean
  protocolPending?: boolean
  onBack: () => void
  onClose: () => void
  onToggleModel: (model: AccountModel, enabled: boolean) => void
  onBulkToggle: (models: string[], enabled: boolean) => void
  onSaveProtocol: (update: AccountModelProtocolUpdate) => void
}) {
  const { t } = useTranslation('sites')
  const isMobile = useMobileLayout()
  const [target, setTarget] = useState<AccountModel | null>(null)
  const [mode, setMode] = useState<ProtocolMode>('inherit')
  const [types, setTypes] = useState<string[]>([])

  const options = target?.supported_endpoint_types ?? []

  function configure(model: AccountModel) {
    const nextMode = model.endpoint_override?.mode ?? 'inherit'
    setTarget(model)
    setMode(nextMode)
    setTypes(
      nextMode === 'allowlist'
        ? (model.endpoint_override?.endpoint_types ?? [])
        : nextMode === 'disabled'
          ? []
          : (model.effective_endpoint_types ?? model.supported_endpoint_types ?? []),
    )
  }

  const items = models.map<ModelsDrawItem>((model) => {
    const name = model.display_name || model.upstream_name
    const protocols = model.effective_endpoint_types ?? model.supported_endpoint_types ?? []
    return {
      id: model.upstream_name,
      displayName: name,
      upstreamName: model.upstream_name,
      protocols: protocols.map((endpointType) => ({ label: formatEndpointTypeLabel(endpointType), enabled: true })),
      enabled: model.enabled,
      icon: modelNameIconInfo(model.upstream_name),
      trailingAction: model.site_model_id ? (
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="h-7 w-7"
          title={t('accountModels.protocol.configure')}
          aria-label={t('accountModels.protocol.configureLabel', { name })}
          onClick={() => configure(model)}
        >
          <Settings2 className="h-4 w-4" />
        </Button>
      ) : undefined,
    }
  })

  const nestedZ = { overlayClassName: 'z-[60]', className: 'z-[60]' }

  return (
    <>
      <ModelsDraw
        open={open}
        title={title}
        items={items}
        backLabel={backLabel}
        onBack={onBack}
        pendingItemId={pendingModel}
        bulkPending={bulkPending}
        shell={isMobile ? 'draw' : 'dialog'}
        nested={!isMobile}
        dialogSize="md"
        dismissLocked={Boolean(target)}
        onToggleItem={(item, enabled) => {
          const model = models.find((candidate) => candidate.upstream_name === item.id)
          if (model) onToggleModel(model, enabled)
        }}
        onBulkToggleItems={(selected, enabled) => {
          const names = selected.filter((item) => item.enabled !== enabled).map((item) => item.id)
          if (names.length) onBulkToggle(names, enabled)
        }}
        onOpenChange={(next) => {
          if (next) return
          if (target && !protocolPending) {
            setTarget(null)
            return
          }
          onClose()
        }}
      />
      <Dialog
        open={Boolean(target)}
        onOpenChange={(next) => {
          if (!next && !protocolPending) setTarget(null)
        }}
      >
        <DialogContent size="sm" {...nestedZ}>
          <DialogHeader>
            <DialogTitle>{t('accountModels.protocol.title')}</DialogTitle>
            <DialogDescription>
              {target
                ? t('accountModels.protocol.namedDescription', { name: target.display_name || target.upstream_name })
                : t('accountModels.protocol.description')}
            </DialogDescription>
          </DialogHeader>
          <DialogBody className="min-h-0 flex-1 space-y-4 overflow-y-auto">
            <p className="text-body text-label-2">
              {mode === 'disabled'
                ? t('accountModels.protocol.modeDisabled')
                : mode === 'allowlist'
                  ? t('accountModels.protocol.modeAllowlist')
                  : t('accountModels.protocol.modeInherit')}
            </p>
            {options.length ? (
              <div className="divide-y divide-separator">
                {options.map((endpointType) => {
                  const checked = types.includes(endpointType)
                  return (
                    <button
                      key={endpointType}
                      type="button"
                      className="flex w-full items-center justify-between gap-3 py-3 text-left first:pt-0 last:pb-0"
                      onClick={() => {
                        setMode('allowlist')
                        setTypes((current) => (
                          checked
                            ? current.filter((item) => item !== endpointType)
                            : current.includes(endpointType) ? current : [...current, endpointType]
                        ))
                      }}
                    >
                      <span className="min-w-0">
                        <span className="block text-body font-medium text-label">{formatEndpointTypeLabel(endpointType)}</span>
                        <span className="block text-footnote text-label-2">{endpointType}</span>
                      </span>
                      <Checkbox
                        checked={checked}
                        className="pointer-events-none"
                        ariaLabel={formatEndpointTypeLabel(endpointType)}
                        onCheckedChange={() => undefined}
                      />
                    </button>
                  )
                })}
              </div>
            ) : (
              <p className="text-body text-label-2">{t('accountModels.protocol.empty')}</p>
            )}
          </DialogBody>
          <DialogFooter
            cancel={(
              <Button variant="secondary" disabled={protocolPending} onClick={() => setTarget(null)}>
                {t('accountModels.cancel')}
              </Button>
            )}
            confirm={(
              <Button
                disabled={!target?.site_model_id || protocolPending || (mode === 'allowlist' && types.length === 0)}
                onClick={() => {
                  if (!target?.site_model_id) return
                  onSaveProtocol({
                    model: target.upstream_name,
                    enabled: target.enabled,
                    siteModelId: target.site_model_id,
                    endpointMode: mode,
                    endpointTypes: types,
                  })
                  setTarget(null)
                }}
              >
                {protocolPending ? <LoaderCircle className="h-4 w-4 animate-spin" /> : null}
                {t('accountModels.save')}
              </Button>
            )}
          />
        </DialogContent>
      </Dialog>
    </>
  )
}
