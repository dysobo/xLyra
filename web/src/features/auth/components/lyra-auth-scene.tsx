import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

type LyraAuthSceneProps = {
  /** 入口按钮的文字（「登录」「创建管理员」）；不传时卡片始终显示、没有关闭按钮（加载 / 出错的状态页） */
  entryLabel?: string
  /** 表单卡片；`close` 把卡片收回成入口按钮 */
  children: (close: () => void) => ReactNode
  /** 注册成功时的高亮标记（浅色主题下改为强调色点缀） */
  vegaLit?: boolean
  /** 初始就展开表单（如登录失效后跳回登录页） */
  defaultOpen?: boolean
  className?: string
}

const EASE = 'duration-700 ease-(--ease-sheet)'
const WIDE = 900
const NARROW_GAP = 24
const NARROW_LIFT = 12

/**
 * 登录 / 注册页场景：纸感网格底 + 字标与标语 + 入口按钮。
 * （已从天琴座夜空改为对齐 img.dysobo.work 的浅色工具站语言）
 */
export function LyraAuthScene({ entryLabel, children, vegaLit = false, defaultOpen = false, className }: LyraAuthSceneProps) {
  const { t } = useTranslation('auth')
  const dismissible = entryLabel !== undefined
  const [open, setOpen] = useState(defaultOpen || !dismissible)
  const [entryReady, setEntryReady] = useState(defaultOpen)
  const [wide, setWide] = useState(true)
  const [spacers, setSpacers] = useState<{ top: number; middle: number } | null>(null)
  const sceneRef = useRef<HTMLDivElement>(null)
  const columnRef = useRef<HTMLDivElement>(null)
  const headerRef = useRef<HTMLElement>(null)
  const entryRef = useRef<HTMLDivElement>(null)
  const cardRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    const timer = window.setTimeout(() => setEntryReady(true), reduceMotion ? 0 : 280)
    return () => window.clearTimeout(timer)
  }, [])

  useLayoutEffect(() => {
    const scene = sceneRef.current
    const column = columnRef.current
    const header = headerRef.current
    const card = cardRef.current
    if (!scene || !column || !header || !card) return

    const measure = () => {
      const isWide = scene.clientWidth >= WIDE
      setWide(isWide)
      if (isWide) {
        setSpacers(null)
        return
      }
      const style = getComputedStyle(column)
      const padTop = Number.parseFloat(style.paddingTop) || 0
      const padBottom = Number.parseFloat(style.paddingBottom) || 0
      const inner = column.clientHeight - padTop - padBottom
      const headerHeight = header.offsetHeight
      const entryHeight = dismissible && entryReady ? (entryRef.current?.scrollHeight ?? 0) : 0
      const closedFree = inner - headerHeight - entryHeight
      const closedMiddle = Math.max(NARROW_GAP, closedFree / 2)
      const closedTop = Math.max(0, closedFree - closedMiddle)
      let top = closedTop
      let middle = closedMiddle
      if (open) {
        const stackedTop = inner - headerHeight - NARROW_GAP - card.offsetHeight
        top = Math.max(0, Math.min(closedTop - NARROW_LIFT, stackedTop))
        middle = NARROW_GAP
      }
      setSpacers({ top, middle })
    }

    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(scene)
    observer.observe(card)
    observer.observe(header)
    return () => observer.disconnect()
  }, [open, entryReady, dismissible])

  const close = () => setOpen(false)
  const narrowSpacers = !wide && spacers
  const topGrow = narrowSpacers ? 'grow-0' : 'grow'
  const middleGrow = narrowSpacers || wide ? 'grow-0' : 'grow'
  const bottomGrow = wide || narrowSpacers ? 'grow' : 'grow-0'

  return (
    <div
      ref={sceneRef}
      className={cn(
        '@container/auth relative size-full overflow-hidden bg-canvas [--auth-safe-top:env(safe-area-inset-top,0px)]',
        className,
      )}
    >
      {/* 纸感网格由 .ambient-background / body canvas 提供；此处加一条顶边青绿细线作为品牌点缀 */}
      <div aria-hidden className="pointer-events-none absolute inset-x-0 top-0 z-10 h-0.5 bg-[var(--sig)]" />

      <div
        ref={columnRef}
        className={cn(
          'relative flex h-full flex-col items-center overflow-y-auto overscroll-contain px-2 pb-[calc(env(safe-area-inset-bottom,0px)+8px)] [scrollbar-width:none]',
          'pt-[calc(var(--auth-safe-top)+56px)]',
          '@min-[900px]/auth:items-start @min-[900px]/auth:px-0 @min-[900px]/auth:pb-0 @min-[900px]/auth:pl-[10%] @min-[900px]/auth:pt-0',
        )}
      >
        <div
          className={cn('shrink-0 transition-[flex-grow,height]', EASE, topGrow)}
          style={narrowSpacers ? { height: narrowSpacers.top } : undefined}
        />

        <header ref={headerRef} className="text-center @min-[900px]/auth:mb-10 @min-[900px]/auth:text-left">
          <h1
            className={cn(
              'font-serif text-[44px] font-medium leading-none tracking-tight text-label @min-[900px]/auth:text-[60px]',
              vegaLit && 'text-[var(--sig)]',
            )}
          >
            x<span className="italic">Lyra</span>
          </h1>
          <div className="mx-auto mt-5 h-px w-10 bg-[var(--sig)] @min-[900px]/auth:mx-0" />
          <p className="mt-5 text-title-3 font-normal text-label">{t('shell.slogan')}</p>
          <p className="mx-auto mt-1.5 max-w-[320px] text-subhead text-label-2 @min-[900px]/auth:mx-0">{t('shell.subtitle')}</p>
        </header>

        <div
          className={cn('shrink-0 transition-[flex-grow,height]', !narrowSpacers && 'min-h-6', EASE, middleGrow)}
          style={narrowSpacers ? { height: narrowSpacers.middle } : undefined}
        />

        {dismissible ? (
          <div
            className={cn(
              'grid w-full max-w-[400px] transition-[grid-template-rows,opacity]',
              EASE,
              !open && entryReady ? 'grid-rows-[1fr] opacity-100' : 'grid-rows-[0fr] opacity-0',
            )}
            aria-hidden={open}
          >
            <div ref={entryRef} className="flex min-h-0 justify-center overflow-hidden @min-[900px]/auth:justify-start">
              <Button variant="default" size="lg" tabIndex={open ? -1 : 0} className="mb-10 mt-1 min-w-[180px] @min-[900px]/auth:mb-1" onClick={() => setOpen(true)}>
                {entryLabel}
              </Button>
            </div>
          </div>
        ) : null}

        <div className={cn('grid w-full max-w-[400px] transition-[grid-template-rows]', EASE, open ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]')}>
          <div className={cn('min-h-0', !open && 'overflow-hidden')}>
            <div
              ref={cardRef}
              className={cn(
                'relative transition-[opacity,transform]',
                EASE,
                open ? 'translate-y-0 opacity-100 delay-100' : 'pointer-events-none translate-y-10 opacity-0',
              )}
              inert={!open}
            >
              {children(close)}
              {dismissible ? (
                <button
                  type="button"
                  aria-label={t('shell.closeForm')}
                  onClick={close}
                  className="absolute right-4 top-4 flex size-8 items-center justify-center rounded-[var(--radius-control)] bg-fill-3 text-label-2 transition-colors hover:bg-fill-2 hover:text-label"
                >
                  <X className="size-4" />
                </button>
              ) : null}
            </div>
          </div>
        </div>

        <div className={cn('transition-[flex-grow]', EASE, bottomGrow)} />
      </div>
    </div>
  )
}
