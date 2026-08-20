import { ref, readonly } from 'vue'

const isDark = ref(
  typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
)

let mediaQueryInitialized = false
let transitionActive = false
let transitionSafetyTimer: ReturnType<typeof setTimeout> | null = null
let activeRipple: HTMLElement | null = null
let transitionGeneration = 0

const TRANSITION_MS = 420

function syncBrowserChrome(dark: boolean) {
  document
    .querySelector<HTMLMetaElement>('meta[name="theme-color"]')
    ?.setAttribute('content', dark ? '#020617' : '#f9fafb')
}

function applyTheme(dark: boolean) {
  isDark.value = dark
  document.documentElement.classList.toggle('dark', dark)
  syncBrowserChrome(dark)
}

function commitToggle() {
  // Read the painted DOM state at click time so a blocked early initializer
  // cannot leave the module-level ref stale on the first toggle.
  const next = !document.documentElement.classList.contains('dark')
  applyTheme(next)
}

function ensureSystemThemeListener() {
  if (mediaQueryInitialized || typeof window === 'undefined') return
  mediaQueryInitialized = true

  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
    applyTheme(e.matches)
  })
}

function syncFromDom() {
  if (typeof document === 'undefined') return
  isDark.value = document.documentElement.classList.contains('dark')
}

function clearTransitionLock(generation: number, ripple: HTMLElement) {
  if (generation !== transitionGeneration) {
    ripple.remove()
    return
  }
  if (transitionSafetyTimer) {
    clearTimeout(transitionSafetyTimer)
    transitionSafetyTimer = null
  }
  ripple.remove()
  if (activeRipple === ripple) activeRipple = null
  transitionActive = false
  document.documentElement.classList.remove('theme-toggling')
}

type TransitionOrigin = { x: number; y: number }

function getVisibleRect(element: HTMLElement) {
  const rect = element.getBoundingClientRect()
  const left = Math.max(0, rect.left)
  const right = Math.min(window.innerWidth, rect.right)
  const top = Math.max(0, rect.top)
  const bottom = Math.min(window.innerHeight, rect.bottom)

  if (rect.width <= 0 || rect.height <= 0 || right <= left || bottom <= top) return null
  return { left, right, top, bottom }
}

function getPointerOrigin(event?: MouseEvent): TransitionOrigin | null {
  if (!event || !Number.isFinite(event.clientX) || !Number.isFinite(event.clientY)) return null
  if (event.clientX < 0 || event.clientX > window.innerWidth) return null
  if (event.clientY < 0 || event.clientY > window.innerHeight) return null

  // Keyboard and synthetic clicks normally report (0, 0). In that case the
  // control center is a more useful and accessible reveal origin.
  if (event.clientX === 0 && event.clientY === 0) return null
  return { x: event.clientX, y: event.clientY }
}

function resolveElementOrigin(element: HTMLElement, pointer: TransitionOrigin | null) {
  const visibleRect = getVisibleRect(element)
  if (!visibleRect) return null

  if (
    pointer &&
    pointer.x >= visibleRect.left &&
    pointer.x <= visibleRect.right &&
    pointer.y >= visibleRect.top &&
    pointer.y <= visibleRect.bottom
  ) {
    return pointer
  }

  return {
    x: (visibleRect.left + visibleRect.right) / 2,
    y: (visibleRect.top + visibleRect.bottom) / 2
  }
}

function getTransitionOrigin(event?: MouseEvent): TransitionOrigin {
  const pointer = getPointerOrigin(event)
  const trigger = event?.currentTarget
  if (trigger instanceof HTMLElement) {
    const origin = resolveElementOrigin(trigger, pointer)
    if (origin) return origin
  }

  // Responsive sidebars remain laid out while translated off-screen. Only use
  // a theme control that actually intersects the viewport; clamping an
  // off-screen center produces the erroneous left-edge reveal after refresh.
  for (const element of document.querySelectorAll<HTMLElement>('[data-theme-toggle]')) {
    const origin = resolveElementOrigin(element, pointer)
    if (origin) return origin
  }

  return { x: window.innerWidth / 2, y: window.innerHeight / 2 }
}

export function useTheme() {
  ensureSystemThemeListener()
  syncFromDom()

  function toggleTheme(event?: MouseEvent) {
    // Lock before any layout read or theme mutation. A compositor-only ripple
    // remains stable under rapid clicks and avoids Chromium root snapshots.
    if (transitionActive) return

    const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (reducedMotion || typeof HTMLElement.prototype.animate !== 'function') {
      commitToggle()
      return
    }

    transitionActive = true
    const generation = ++transitionGeneration
    const { x, y } = getTransitionOrigin(event)
    const endRadius = Math.hypot(
      Math.max(x, window.innerWidth - x),
      Math.max(y, window.innerHeight - y)
    )

    const goingDark = !document.documentElement.classList.contains('dark')
    document.documentElement.classList.add('theme-toggling')
    commitToggle()

    const ripple = document.createElement('span')
    ripple.dataset.themeRipple = ''
    ripple.dataset.themeRippleX = `${x}`
    ripple.dataset.themeRippleY = `${y}`
    ripple.className = 'theme-ripple-overlay'
    ripple.style.backgroundColor = goingDark ? '#020617' : '#f9fafb'
    document.body.appendChild(ripple)
    activeRipple = ripple

    try {
      const animation = ripple.animate(
        [
          { clipPath: `circle(0px at ${x}px ${y}px)`, opacity: 0.34 },
          {
            clipPath: `circle(${endRadius * 0.82}px at ${x}px ${y}px)`,
            opacity: 0.12,
            offset: 0.72
          },
          { clipPath: `circle(${endRadius}px at ${x}px ${y}px)`, opacity: 0 }
        ],
        {
          duration: TRANSITION_MS,
          easing: 'cubic-bezier(0.2, 0, 0, 1)',
          fill: 'both'
        }
      )
      const finish = () => clearTransitionLock(generation, ripple)
      transitionSafetyTimer = setTimeout(finish, TRANSITION_MS + 250)
      animation.finished.catch(() => {}).finally(finish)
    } catch {
      clearTransitionLock(generation, ripple)
    }
  }

  return { isDark: readonly(isDark), toggleTheme, applyTheme }
}
