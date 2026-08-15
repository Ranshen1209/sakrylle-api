import { ref, readonly } from 'vue'

const isDark = ref(
  typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
)

let mediaQueryInitialized = false
let transitionActive = false
let transitionSafetyTimer: ReturnType<typeof setTimeout> | null = null

const TRANSITION_MS = 500

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
  localStorage.setItem('theme', next ? 'dark' : 'light')
}

function ensureSystemThemeListener() {
  if (mediaQueryInitialized || typeof window === 'undefined') return
  mediaQueryInitialized = true

  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
    if (!localStorage.getItem('theme')) {
      applyTheme(e.matches)
    }
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
    // Lock before any layout read or theme mutation. Chromium can crash when
    // multiple full-viewport animations are created by rapid clicks.
    if (transitionActive) return

    const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (reducedMotion || typeof HTMLElement.prototype.animate !== 'function') {
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
    document.documentElement.classList.add('theme-toggling')
    commitToggle()

    const ripple = document.createElement('span')
    ripple.dataset.themeRipple = ''
    ripple.className = 'theme-ripple-overlay'
    ripple.style.left = `${x - endRadius}px`
    ripple.style.top = `${y - endRadius}px`
    ripple.style.width = `${endRadius * 2}px`
    ripple.style.height = `${endRadius * 2}px`
    ripple.style.backgroundColor = goingDark ? '#020617' : '#f9fafb'
    document.body.appendChild(ripple)

    try {
      const animation = ripple.animate(
        [
          { transform: 'scale(0)', opacity: 0.28 },
          { transform: 'scale(1)', opacity: 0 }
        ],
        { duration: TRANSITION_MS, easing: 'ease-out', fill: 'both' }
      )
      const finish = () => {
        ripple.remove()
        clearTransitionLock()
      }
      transitionSafetyTimer = setTimeout(finish, TRANSITION_MS + 250)
      animation.finished.catch(() => {}).finally(finish)
    } catch {
      ripple.remove()
      clearTransitionLock()
    }
  }

  return { isDark: readonly(isDark), toggleTheme, applyTheme }
}
