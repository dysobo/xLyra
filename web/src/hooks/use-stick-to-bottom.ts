import { useCallback, useEffect, useRef, useState } from 'react'

/** 距底不超过该值视为贴底，恢复自动跟随 */
const STICK_THRESHOLD_PX = 40
/** 距底超过该值才显示「回到最新」入口，避免轻微回滚就闪按钮 */
const JUMP_THRESHOLD_PX = 120
/** 触屏判定「朝旧内容方向拖动」的单次位移 */
const TOUCH_DETACH_SLOP_PX = 12
/** 与最近一次程序化贴底落点的容差，小于它视为自身回声 */
const ECHO_EPSILON_PX = 2

/** scroll 事件与最近一次程序化赋值的落点一致（±容差）时视为自身回声，
 *  不参与「用户是否滚离」判定——纯位置推断会把贴底误读成用户滚离 */
export function isProgrammaticEcho(scrollTop: number, programmaticTop: number | null) {
  return programmaticTop !== null && Math.abs(scrollTop - programmaticTop) < ECHO_EPSILON_PX
}

/** wheel 朝旧内容方向的滚动意图：向上滚（deltaY 为负）即锁定 */
export function isScrollAwayIntent(deltaY: number) {
  return deltaY < 0
}

/** 触屏朝旧内容方向的拖动意图：手指下移（位移为正）即锁定，与 wheel 方向约定相反；
 *  位移需超过容忍值，过滤点击与轻微抖动 */
export function isTouchDragAway(deltaY: number) {
  return deltaY > TOUCH_DETACH_SLOP_PX
}

/** scroll 事件后的贴底状态迁移。意图锁定必须经得起随后的事件：
 *  用户向上小幅回滚（仍在贴底阈值内、方向朝上）时保持锁定，否则会被内容增长
 *  反复拽回底部；只有朝底部方向滚动且落点进入阈值才恢复跟随。 */
export function nextStickState(current: boolean, scrolledDown: boolean, atBottom: boolean) {
  if (scrolledDown && atBottom) return true
  if (!atBottom) return false
  return current
}

/**
 * 流式输出的贴底跟随：贴底时内容增长自动滚到底；用户朝旧内容方向滚动即锁定，
 * 增量静默渲染不打扰阅读；滚回底部（或点击「回到最新」/发送新消息）恢复跟随。
 *
 * 锁定不靠纯位置推断：程序化赋值 scrollTop 同样会触发 scroll 事件，若事件派发时
 * 内容又长高了，纯位置判定会把自己的贴底误读成「用户滚离」导致跟随随机中断。
 * 因此锁定由 wheel/touchmove/keydown 的滚动意图触发；scroll 事件带方向感知——
 * 朝底部滚动且落点进入阈值才恢复跟随（小幅回滚不打断锁定），并在与最近一次
 * 程序化赋值落点一致（自身回声）时跳过判定。
 */
export function useStickToBottom() {
  const [element, setElement] = useState<HTMLElement | null>(null)
  const [showJump, setShowJump] = useState(false)
  const stuckRef = useRef(true)
  const programmaticTopRef = useRef<number | null>(null)

  const scrollRef = useCallback((node: HTMLElement | null) => {
    setElement(node)
  }, [])

  /** 强制贴底并恢复自动跟随（发送消息 / 切换会话 / 点击「回到最新」） */
  const scrollToBottom = useCallback(() => {
    stuckRef.current = true
    setShowJump(false)
    if (!element) return
    element.scrollTo({ top: element.scrollHeight })
    programmaticTopRef.current = element.scrollTop
  }, [element])

  useEffect(() => {
    if (!element) return
    // 新元素没有程序化滚动历史，清掉旧元素的落点，避免跨元素误判回声
    programmaticTopRef.current = null
    const distance = () => element.scrollHeight - element.scrollTop - element.clientHeight
    const detach = () => {
      stuckRef.current = false
    }
    // 上一次已知的滚动位置：scroll 事件据此判断滚动方向（朝底/朝旧内容），
    // 程序化贴底后也要同步，否则用户随后的回滚方向会被误判
    let lastTop = element.scrollTop
    // 最近一次用户滚动是否朝底：scrollend 恢复跟随的资格条件
    let lastMovedDown = false
    const pinToBottom = () => {
      element.scrollTop = element.scrollHeight
      programmaticTopRef.current = element.scrollTop
      lastTop = element.scrollTop
    }
    const isEcho = () => isProgrammaticEcho(element.scrollTop, programmaticTopRef.current)

    const onScroll = () => {
      const prevTop = lastTop
      lastTop = element.scrollTop
      if (isEcho()) return
      const movedDown = element.scrollTop > prevTop
      lastMovedDown = movedDown
      stuckRef.current = nextStickState(stuckRef.current, movedDown, distance() <= STICK_THRESHOLD_PX)
      setShowJump(distance() > JUMP_THRESHOLD_PX)
    }

    // 用户朝旧内容方向的主动滚动意图：立即锁定，不等越过距离阈值，
    // 否则阈值内的回滚会被自动跟随反复拽回
    const onWheel = (event: WheelEvent) => {
      // ctrl+wheel / 触控板双指缩放不是滚动意图
      if (event.ctrlKey) return
      if (isScrollAwayIntent(event.deltaY)) detach()
    }
    let touchLastY: number | null = null
    const onTouchStart = (event: TouchEvent) => {
      touchLastY = event.touches[0]?.clientY ?? null
    }
    const onTouchMove = (event: TouchEvent) => {
      const y = event.touches[0]?.clientY
      if (touchLastY !== null && y !== undefined && isTouchDragAway(y - touchLastY)) detach()
      touchLastY = y ?? null
    }
    const onTouchEnd = () => {
      touchLastY = null
    }
    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target as Node | null
      // 只在键盘滚动确实作用于本滚动区时（焦点在其中且非输入控件）才视为意图，
      // 避免焦点在页面其他位置按 ↑/PageUp 时无声地停掉自动跟随
      if (!target || target === element || !element.contains(target)) return
      if (target instanceof HTMLElement && target.closest('input, textarea, select, [contenteditable="true"], [contenteditable=""]')) return
      if (event.key === 'ArrowUp' || event.key === 'PageUp' || event.key === 'Home') detach()
    }

    // 内容 / 视口高度变化时贴底。ResizeObserver 回调在布局后、绘制前执行，
    // 在这里赋值 scrollTop 不会出现「先渲染未贴底内容再跳一下」的抖动；
    // 图片加载、公式渲染、语法高亮这类不经过 React 状态更新的高度变化也被覆盖
    const onResize = () => {
      if (stuckRef.current) {
        pinToBottom()
        return
      }
      setShowJump(distance() > JUMP_THRESHOLD_PX)
    }
    const observer = new ResizeObserver(onResize)
    observer.observe(element)
    if (element.firstElementChild) observer.observe(element.firstElementChild)

    // 滚动收尾时若最近一段位移朝底且落点已在阈值内 → 贴齐底部并恢复跟随。
    // 必须带方向条件：向上小幅回看（落点仍在阈值内）一停手就被拽回底部的
    // 话，意图锁定形同虚设。方向的兜底场景是惯性滚动恰好停在过期的程序化
    // 落点上——那一次 scroll 事件会被回声判定跳过，靠这里恢复跟随
    const onScrollEnd = () => {
      if (!lastMovedDown || distance() > STICK_THRESHOLD_PX) return
      stuckRef.current = true
      setShowJump(false)
      pinToBottom()
    }

    element.addEventListener('scroll', onScroll, { passive: true })
    element.addEventListener('wheel', onWheel, { passive: true })
    element.addEventListener('touchstart', onTouchStart, { passive: true })
    element.addEventListener('touchmove', onTouchMove, { passive: true })
    element.addEventListener('touchend', onTouchEnd, { passive: true })
    element.addEventListener('scrollend', onScrollEnd, { passive: true })
    window.addEventListener('keydown', onKeyDown)

    return () => {
      element.removeEventListener('scroll', onScroll)
      element.removeEventListener('wheel', onWheel)
      element.removeEventListener('touchstart', onTouchStart)
      element.removeEventListener('touchmove', onTouchMove)
      element.removeEventListener('touchend', onTouchEnd)
      element.removeEventListener('scrollend', onScrollEnd)
      window.removeEventListener('keydown', onKeyDown)
      observer.disconnect()
    }
  }, [element])

  return { scrollRef, scrollToBottom, showJump }
}
