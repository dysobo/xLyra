import { useEffect, useRef } from 'react'
import { useLocation } from 'react-router-dom'
import { mulberry32 } from '@/features/auth/components/lyra-sky-layers'

/**
 * 全局背景（L0）：
 * - 主站：纸感网格（index.css .ambient-background）
 * - Agent：上游原版星空（保持暗色岛观感）
 */

const TINTS = ['226,232,255', '226,232,255', '236,240,255', '208,222,255', '255,244,228']
const CELL = 30

const smooth = (value: number) => value * value * (3 - 2 * value)

function StarfieldAmbient() {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    const context = canvas?.getContext('2d')
    if (!canvas || !context) return

    const draw = () => {
      const dpr = Math.min(window.devicePixelRatio || 1, 2)
      const width = canvas.clientWidth
      const height = canvas.clientHeight
      canvas.width = Math.max(1, Math.round(width * dpr))
      canvas.height = Math.max(1, Math.round(height * dpr))
      context.setTransform(dpr, 0, 0, dpr, 0, 0)
      context.clearRect(0, 0, width, height)

      const random = mulberry32(20261018)
      const waves = Array.from({ length: 3 }, () => ({
        fx: (0.6 + random() * 1.4) / Math.max(width, 1) * Math.PI * 2,
        fy: (0.6 + random() * 1.4) / Math.max(height, 1) * Math.PI * 2,
        px: random() * Math.PI * 2,
        py: random() * Math.PI * 2,
      }))
      const onePixel = 1 / dpr
      for (let cy = 0; cy < height; cy += CELL) {
        for (let cx = 0; cx < width; cx += CELL) {
          const x = cx + random() * CELL
          const y = cy + random() * CELL
          const roll = random()
          const brightness = random()
          const tint = TINTS[Math.floor(random() * TINTS.length)]
          const size = random()

          let field = 0
          for (const wave of waves) field += 0.5 + 0.5 * Math.sin(x * wave.fx + wave.px) * Math.sin(y * wave.fy + wave.py)
          field /= waves.length
          const dx = (x / width - 0.5) * 2
          const dy = (y / height - 0.5) * 2
          const edge = smooth(Math.min(1, Math.hypot(dx, dy) / 1.25))
          const probability = 0.34 * (0.35 + 0.9 * field) * (0.4 + 0.9 * edge)
          if (roll > probability) continue

          const alpha = 0.07 + brightness ** 2.4 * 0.36
          const radius = size > 0.96 ? onePixel * 1.7 : Math.max(onePixel, 0.55)
          context.fillStyle = 
gba(, )
          context.beginPath()
          context.arc(x, y, radius, 0, Math.PI * 2)
          context.fill()
        }
      }
    }

    draw()
    let timer = 0
    const onResize = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(draw, 120)
    }
    window.addEventListener('resize', onResize)
    return () => {
      window.clearTimeout(timer)
      window.removeEventListener('resize', onResize)
    }
  }, [])

  return (
    <div className=ambient-background aria-hidden=true>
      <canvas ref={canvasRef} className=size-full />
    </div>
  )
}

export function AmbientBackground() {
  const location = useLocation()
  const isAgentWorkspace = location.pathname === '/agent' || location.pathname.startsWith('/agent/')
  if (isAgentWorkspace) return <StarfieldAmbient />
  return <div className=ambient-background aria-hidden=true />
}
