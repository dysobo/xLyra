import type { CSSProperties } from 'react'

/**
 * 图表主题。recharts 把颜色写进 SVG 属性，
 * 这里给出与 `--chart-1…8` 相同的十六进制值，方便在 JS 里取用、拼透明度。
 * 顺序即系列顺序：蓝、青、绿、橙、紫、粉、黄、靛。
 */
export const chartColors = ['#0b7f96', '#22c3e0', '#1f8a4c', '#a86f00', '#6b5b95', '#c2305a', '#e8a800', '#3d5a80']

/** 网格线：只画水平线，用分隔线颜色 */
export const chartGridStroke = 'var(--separator)'

/** 坐标轴文字：Caption 2（11）、label-3，数字等宽 */
export const chartTick = { fill: 'var(--label-tertiary)', fontSize: 11, fontFamily: 'var(--font-mono)' }

/** 悬停时的柱状背景条 */
export const chartCursorFill = 'var(--fill-quaternary)'

/** 环形图扇区之间的分隔描边：与卡片底色一致，看起来像留了缝 */
export const chartSliceStroke = 'var(--bg-elevated-1)'

/** Tooltip：白底细线（纸感） */
export const chartTooltipStyle: CSSProperties = {
  border: '1px solid var(--separator)',
  borderRadius: 6,
  background: 'var(--bg-elevated-1)',
  boxShadow: '0 8px 24px rgb(24 32 43 / 0.12)',
  color: 'var(--label-primary)',
  padding: '8px 12px',
  fontSize: 12,
  lineHeight: 1.35,
}

export const chartTooltipLabelStyle: CSSProperties = {
  color: 'var(--label-secondary)',
  fontSize: 12,
  marginBottom: 4,
}

export const chartTooltipItemStyle: CSSProperties = {
  color: 'var(--label-primary)',
  fontSize: 12,
  padding: '1px 0',
}
