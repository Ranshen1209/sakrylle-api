import { nextTick, ref, readonly } from 'vue'

const isDark = ref(
  typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
)

let mediaQueryInitialized = false
let transitionActive = false
let transitionSafetyTimer: ReturnType<typeof setTimeout> | null = null

const TRANSITION_MS = 500

type ThemeViewTransition = {
  ready: Promise<void>
}

type ViewTransitionDocument = Document & {
  startViewTransition?: (update: () => void | Promise<void>) => ThemeViewTransition
}

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

function clearTransitionLock() {
  if (transitionSafetyTimer) {
    clearTimeout(transitionSafetyTimer)
    transitionSafetyTimer = null
  }
  transitionActive = false
  document.documentElement.classList.remove('theme-toggling')
  document.documentElement.style.removeProperty('--theme-transition-bg')
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

function resolveElementOrigin(element: HTMLElement) {
  const visibleRect = getVisibleRect(element)
  if (!visibleRect) return null

  return {
    x: (visibleRect.left + visibleRect.right) / 2,
    y: (visibleRect.top + visibleRect.bottom) / 2
  }
}

function getTransitionOrigin(trigger?: EventTarget | null): TransitionOrigin {
  if (trigger instanceof HTMLElement) {
    const origin = resolveElementOrigin(trigger)
    if (origin) return origin
  }

  // Responsive sidebars remain laid out while translated off-screen. Only use
  // a theme control that actually intersects the viewport; clamping an
  // off-screen center produces the erroneous left-edge reveal after refresh.
  for (const element of document.querySelectorAll<HTMLElement>('[data-theme-toggle]')) {
    const origin = resolveElementOrigin(element)
    if (origin) return origin
  }

  return { x: window.innerWidth / 2, y: window.innerHeight / 2 }
}

export function useTheme() {
  ensureSystemThemeListener()
  syncFromDom()

  function toggleTheme(event?: MouseEvent) {
    // A single root snapshot transition is expensive enough that overlapping
    // toggles can visibly tear, especially on mobile Chromium.
    if (transitionActive) return

    const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    const transitionDocument = document as ViewTransitionDocument
    if (
      reducedMotion ||
      typeof transitionDocument.startViewTransition !== 'function' ||
      typeof document.documentElement.animate !== 'function'
    ) {
      commitToggle()
      return
    }

    transitionActive = true
    const { x, y } = getTransitionOrigin(event?.currentTarget)
    const endRadius = Math.hypot(
      Math.max(x, window.innerWidth - x),
      Math.max(y, window.innerHeight - y)
    )

    const goingDark = !document.documentElement.classList.contains('dark')
    document.documentElement.style.setProperty(
      '--theme-transition-bg',
      goingDark ? '#020617' : '#f9fafb'
    )
    document.documentElement.classList.add('theme-toggling')

    try {
      const transition = transitionDocument.startViewTransition(async () => {
        commitToggle()
        await nextTick()
      })
      transitionSafetyTimer = setTimeout(clearTransitionLock, TRANSITION_MS + 500)

      transition.ready
        .then(() => {
          const animation = document.documentElement.animate(
            {
              clipPath: [
                `circle(0px at ${x}px ${y}px)`,
                `circle(${endRadius}px at ${x}px ${y}px)`
              ]
            },
            {
              duration: TRANSITION_MS,
              easing: 'cubic-bezier(0.2, 0, 0, 1)',
              fill: 'both',
              pseudoElement: '::view-transition-new(root)'
            }
          )
          return animation.finished.catch(() => {})
        })
        .catch(() => {})
        .finally(clearTransitionLock)
    } catch {
      commitToggle()
      clearTransitionLock()
    }
  }

  return { isDark: readonly(isDark), toggleTheme, applyTheme }
}
