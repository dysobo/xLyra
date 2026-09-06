import { describe, expect, it } from 'vitest'
import { isProgrammaticEcho, isScrollAwayIntent, isTouchDragAway, nextStickState } from './use-stick-to-bottom'

describe('isProgrammaticEcho', () => {
  it('treats scroll events within tolerance of the last programmatic top as echoes', () => {
    expect(isProgrammaticEcho(1000, 1000)).toBe(true)
    expect(isProgrammaticEcho(1001.5, 1000)).toBe(true)
    expect(isProgrammaticEcho(998.5, 1000)).toBe(true)
  })

  it('treats genuinely different positions as user scrolls', () => {
    expect(isProgrammaticEcho(1003, 1000)).toBe(false)
    expect(isProgrammaticEcho(998, 1000)).toBe(false)
    expect(isProgrammaticEcho(500, 1000)).toBe(false)
  })

  it('never reports an echo before any programmatic scroll happened', () => {
    expect(isProgrammaticEcho(0, null)).toBe(false)
  })
})

describe('isScrollAwayIntent', () => {
  it('detaches on upward wheel scrolling toward older content', () => {
    expect(isScrollAwayIntent(-3)).toBe(true)
  })

  it('keeps following on downward or horizontal-only wheel input', () => {
    expect(isScrollAwayIntent(5)).toBe(false)
    expect(isScrollAwayIntent(0)).toBe(false)
  })
})

describe('isTouchDragAway', () => {
  it('detaches when the finger drags down toward older content past the slop', () => {
    expect(isTouchDragAway(13)).toBe(true)
  })

  it('ignores small movements and upward drags (opposite sign convention to wheel)', () => {
    expect(isTouchDragAway(5)).toBe(false)
    expect(isTouchDragAway(0)).toBe(false)
    expect(isTouchDragAway(-30)).toBe(false)
  })
})

describe('nextStickState', () => {
  it('re-engages following when scrolling down lands within the stick threshold', () => {
    expect(nextStickState(false, true, true)).toBe(true)
  })

  it('keeps intent-based lock through small upward scrolls still inside the threshold', () => {
    // 用户向上回滚 30px（距底仍在 40px 内）：不能因落点贴底就恢复跟随，
    // 否则内容增长会把用户反复拽回底部
    expect(nextStickState(false, false, true)).toBe(false)
  })

  it('detaches positionally once the user is outside the threshold, whatever the direction', () => {
    expect(nextStickState(true, false, false)).toBe(false)
    expect(nextStickState(true, true, false)).toBe(false)
  })

  it('leaves the current state alone while at the bottom without downward movement', () => {
    expect(nextStickState(true, false, true)).toBe(true)
  })
})
