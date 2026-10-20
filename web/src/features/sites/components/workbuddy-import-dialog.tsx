import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { LoaderCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { JsonFileDropArea, JsonFileList } from '@/components/common/json-file-drop'
import { useJsonFiles } from '@/components/common/use-json-files'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogBody, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useNestedDialogClassNames } from '@/features/sites/components/use-nested-dialog'
import { importWorkBuddyAccounts, type WorkBuddyImportResponse } from '@/features/sites/api/sites'
import { toast } from '@/lib/toast'

/**
 * Imports WorkBuddy accounts from exported JSON files. The files are read in the
 * browser and uploaded as text; the server detects the China / international
 * site of every account from its realm and domain.
 */
export function WorkBuddyImportDialog({
  open,
  siteId,
  regionLabel,
  onOpenChange,
  onImported,
}: {
  open: boolean
  siteId: string
  regionLabel: (regionId: string) => string
  onOpenChange: (open: boolean) => void
  onImported: () => void | Promise<void>
}) {
  const { t } = useTranslation('sites')
  const nestedZ = useNestedDialogClassNames()
  const { files, addFiles, removeFile, clearFiles } = useJsonFiles()
  const [result, setResult] = useState<WorkBuddyImportResponse | null>(null)

  const importMutation = useMutation({
    mutationFn: async () => {
      const documents = await Promise.all(files.map(async (item) => ({ name: item.file.name, content: await item.file.text() })))
      return importWorkBuddyAccounts(siteId, documents)
    },
    onSuccess: async (response) => {
      setResult(response)
      await onImported()
      if (response.meta.failed > 0) {
        toast.warning(t('workbuddyAccounts.importDialog.toastDone'), {
          description: t('workbuddyAccounts.importDialog.summary', response.meta),
        })
      } else {
        toast.success(t('workbuddyAccounts.importDialog.toastDone'))
      }
    },
    onError: (error) => toast.error(t('workbuddyAccounts.importDialog.toastFailed'), { description: error.message }),
  })

  function handleOpenChange(next: boolean) {
    if (!next && importMutation.isPending) return
    onOpenChange(next)
    if (!next) {
      clearFiles()
      setResult(null)
      importMutation.reset()
    }
  }

  const pending = importMutation.isPending

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        presentation="center"
        size="md"
        overlayClassName={nestedZ.overlayClassName}
        className={nestedZ.className}
        onOpenAutoFocus={(event) => event.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{t('workbuddyAccounts.importDialog.title')}</DialogTitle>
        </DialogHeader>
        <DialogBody className="flex min-w-0 max-w-full flex-col gap-4 overflow-hidden">
          {result ? (
            <div className="flex min-h-0 flex-1 flex-col gap-4">
              <div className="rounded-inner border border-separator bg-fill-3 px-4 py-3 text-body">
                {t('workbuddyAccounts.importDialog.summary', result.meta)}
              </div>
              <div className="min-h-0 flex-1 space-y-2 overflow-y-auto pr-1">
                {result.items.map((item, index) => (
                  <div key={`${item.source}-${index}`} className="space-y-0.5 rounded-inner border border-separator bg-elevated-1 px-3 py-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-body font-medium text-label">{item.label || t('workbuddyAccounts.importDialog.unnamed')}</span>
                      {item.region ? <Badge variant="neutral" className="text-footnote">{regionLabel(item.region)}</Badge> : null}
                      <Badge variant={item.status === 'imported' ? 'success' : 'warning'} className="text-footnote">
                        {t(item.status === 'imported' ? 'workbuddyAccounts.importDialog.imported' : 'workbuddyAccounts.importDialog.failed')}
                      </Badge>
                    </div>
                    <div className="truncate text-footnote text-label-2">{item.source}</div>
                    {item.error ? <div className="text-footnote text-danger">{item.error}</div> : null}
                    {item.warning ? <div className="text-footnote text-warning">{item.warning}</div> : null}
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <div className="flex min-h-0 flex-1 flex-col gap-4">
              <JsonFileDropArea
                onFiles={addFiles}
                active={open}
                disabled={pending}
                title={t('workbuddyAccounts.importDialog.dropzone')}
                hint={t('workbuddyAccounts.importDialog.hint')}
              />
              <JsonFileList
                files={files}
                countLabel={t('workbuddyAccounts.importDialog.selectedCount', { count: files.length })}
                onRemove={removeFile}
              />
            </div>
          )}
        </DialogBody>
        <DialogFooter
          cancel={result ? undefined : (
            <Button variant="secondary" disabled={pending} onClick={() => handleOpenChange(false)}>
              {t('workbuddyAccounts.importDialog.cancel')}
            </Button>
          )}
          confirm={result ? (
            <Button onClick={() => handleOpenChange(false)}>{t('workbuddyAccounts.importDialog.done')}</Button>
          ) : (
            <Button disabled={files.length === 0 || pending} onClick={() => importMutation.mutate()}>
              {pending ? <LoaderCircle className="h-4 w-4 animate-spin" /> : null}
              {files.length > 0
                ? t('workbuddyAccounts.importDialog.importWithCount', { count: files.length })
                : t('workbuddyAccounts.importDialog.import')}
            </Button>
          )}
        />
      </DialogContent>
    </Dialog>
  )
}
