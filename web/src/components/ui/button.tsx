import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { LoaderCircle } from 'lucide-react'
import { useLiquidGlass } from '@/components/glass/use-liquid-glass'
import { cn } from '@/lib/utils'

/**
 * 按钮。
 * - 胶囊形；高度取自 --control-h-*，移动端自动放大到 44 等触控尺寸。
 * - 不做 hover 上浮和投影；按下时轻微缩小。
 * - 强调色用在背景上：default（主按钮）是浅色底、深色字，每个视图最多一个。
 */
const buttonVariants = cva(
  'inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-[var(--radius-control)] font-medium select-none transition-[transform,background-color,color,border-color,filter,opacity] duration-150 ease-out focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-tint/40 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas active:scale-[0.97] disabled:pointer-events-none disabled:opacity-40 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        /** 主按钮：青绿强调 */
        default: 'bg-tint text-tint-foreground hover:brightness-[1.06] active:brightness-95',
        /** 次要按钮：填充色底 */
        secondary: 'bg-fill-2 text-label hover:bg-fill-1',
        /** 描边按钮 */
        outline: 'border border-separator bg-elevated-1 text-label hover:bg-fill-4',
        /** 纯文字按钮 */
        ghost: 'bg-transparent text-label hover:bg-fill-4',
        /** 破坏性操作的最终确认 */
        destructive: 'bg-danger text-white hover:brightness-110 active:brightness-95',
        /** 普通位置的破坏性操作 */
        'destructive-soft': 'bg-danger/14 text-danger hover:bg-danger/22',
        /** 磨砂面板按钮 */
        glass: 'lg-regular text-label hover:bg-fill-4',
      },
      size: {
        default: 'h-[var(--control-h)] px-4 text-body [&_svg]:size-4',
        sm: 'h-[var(--control-h-sm)] px-3 text-callout [&_svg]:size-3.5',
        lg: 'h-[var(--control-h-lg)] px-5 text-body [&_svg]:size-[18px]',
        icon: 'size-[var(--control-h)] [&_svg]:size-[18px]',
        'icon-sm': 'size-[var(--control-h-sm)] [&_svg]:size-4',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)

interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
  /** 进行中：显示转圈并禁用点击（asChild 时不插入图标） */
  loading?: boolean
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, loading = false, disabled, type = 'button', children, ...props }, ref) => {
    const innerRef = React.useRef<HTMLButtonElement>(null)
    React.useImperativeHandle(ref, () => innerRef.current as HTMLButtonElement)
    useLiquidGlass(innerRef, 'regular', variant === 'glass')

    const Comp = asChild ? Slot : 'button'

    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={innerRef}
        type={asChild ? undefined : type}
        disabled={disabled || loading}
        aria-busy={loading || undefined}
        {...props}
      >
        {/* Slot 只接受单个子元素，asChild 时不插入加载图标 */}
        {asChild ? (
          children
        ) : (
          <>
            {loading ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
            {children}
          </>
        )}
      </Comp>
    )
  },
)
Button.displayName = 'Button'

export { Button }
