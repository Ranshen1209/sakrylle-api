import { nextTick, ref, readonly } from 'vue'

const isDark = ref(
  typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
)

type ThemeViewTransition = {
  ready: Promise<void>
  finished: Promise<void>
  updateCallbackDone?: Promise<void>
  skipTransition?: () => void
}

type ViewTransitionDocument = Document & {
  startViewTransition?: (update: () => void | Promise<void>) => ThemeViewTransition
}

type ThemeTransitionRun = {
  transition: ThemeViewTransition | null
  safetyTimer: ReturnType<typeof setTimeout> | null
  skipRequested: boolean
}

let mediaQueryInitialized = false
let activeTransition: ThemeTransitionRun | null = null
let pendingSystemTheme: boolean | null = null
let nativeTransitionsDisabled = false

const TRANSITION_MS = 500
const TRANSITION_SAFETY_MS = TRANSITION_MS + 1500
const TRANSITION_COOLDOWN_MS = 80

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
  applyTheme(!document.documentElement.classList.contains('dark'))
}

function ensureSystemThemeListener() {
  if (mediaQueryInitialized || typeof window === 'undefined') return
  mediaQueryInitialized = true

  const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
  const handleChange = (event: MediaQueryListEvent) => {
    if (activeTransition) {
      pendingSystemTheme = event.matches
      return
    }
    applyTheme(event.matches)
  }

  if (typeof mediaQuery.addEventListener === 'function') {
    mediaQuery.addEventListener('change', handleChange)
  } else if (typeof mediaQuery.addListener === 'function') {
    mediaQuery.addListener(handleChange)
  }
}

function syncFromDom() {
  if (typeof document === 'undefined') return
  isDark.value = document.documentElement.classList.contains('dark')
}

function clearTransition(run: ThemeTransitionRun) {
  if (activeTransition !== run) return

  if (run.safetyTimer) {
    clearTimeout(run.safetyTimer)
    run.safetyTimer = null
  }

  activeTransition = null
  document.documentElement.classList.remove('theme-toggling')
  document.documentElement.style.removeProperty('--theme-transition-bg')
  document.documentElement.style.removeProperty('--theme-transition-x')
  document.documentElement.style.removeProperty('--theme-transition-y')

  if (pendingSystemTheme !== null) {
    const browserTheme = pendingSystemTheme
    pendingSystemTheme = null
    applyTheme(browserTheme)
  }
}

function scheduleTransitionCleanup(run: ThemeTransitionRun) {
  if (activeTransition !== run) return

  if (run.safetyTimer) {
    clearTimeout(run.safetyTimer)
    run.safetyTimer = null
  }

  const finishAfterCooldown = () => {
    setTimeout(() => clearTransition(run), TRANSITION_COOLDOWN_MS)
  }

  if (typeof window.requestAnimationFrame === 'function') {
    window.requestAnimationFrame(() => window.requestAnimationFrame(finishAfterCooldown))
    return
  }
  finishAfterCooldown()
}

function requestTransitionSkip(run: ThemeTransitionRun) {
  if (activeTransition !== run || run.skipRequested) return
  run.skipRequested = true
  nativeTransitionsDisabled = true

  try {
    run.transition?.skipTransition?.()
  } catch {
    // ViewTransition.finished remains the lifecycle owner even if skip fails.
  }
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

  // Keyboard and synthetic clicks normally report (0, 0). Use the control
  // center for those instead of revealing from the viewport's top-left corner.
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
  // a theme control that actually intersects the viewport.
  for (const element of document.querySelectorAll<HTMLElement>('[data-theme-toggle]')) {
    const origin = resolveElementOrigin(element, pointer)
    if (origin) return origin
  }

  return pointer ?? { x: window.innerWidth / 2, y: window.innerHeight / 2 }
}

export function useTheme() {
  ensureSystemThemeListener()
  syncFromDom()

  function toggleTheme(event?: MouseEvent) {
    // Ignore repeat clicks until Chromium has torn down the current root
    // snapshots. Starting another transition sooner can crash the renderer.
    if (activeTransition) return

    const transitionDocument = document as ViewTransitionDocument
    const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (
      reducedMotion ||
      nativeTransitionsDisabled ||
      typeof transitionDocument.startViewTransition !== 'function' ||
      typeof document.documentElement.animate !== 'function'
    ) {
      commitToggle()
      return
    }

    const { x, y } = getTransitionOrigin(event)
    const endRadius = Math.hypot(
      Math.max(x, window.innerWidth - x),
      Math.max(y, window.innerHeight - y)
    )
    const nextDark = !document.documentElement.classList.contains('dark')
    const run: ThemeTransitionRun = {
      transition: null,
      safetyTimer: null,
      skipRequested: false
    }
    let themeCommitted = false

    activeTransition = run
    document.documentElement.style.setProperty(
      '--theme-transition-bg',
      nextDark ? '#020617' : '#f9fafb'
    )
    document.documentElement.style.setProperty('--theme-transition-x', `${x}px`)
    document.documentElement.style.setProperty('--theme-transition-y', `${y}px`)
    document.documentElement.classList.add('theme-toggling')

    try {
      const transition = transitionDocument.startViewTransition(async () => {
        applyTheme(nextDark)
        themeCommitted = true
        await nextTick()
      })
      run.transition = transition
      run.safetyTimer = setTimeout(() => requestTransitionSkip(run), TRANSITION_SAFETY_MS)

      void transition.updateCallbackDone?.catch(() => {})
      void transition.ready
        .then(() => {
          if (activeTransition !== run) return
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
          void animation.finished.catch(() => {})
        })
        .catch(() => requestTransitionSkip(run))

      void transition.finished
        .catch(() => {})
        .finally(() => scheduleTransitionCleanup(run))
    } catch {
      nativeTransitionsDisabled = true
      if (!themeCommitted) applyTheme(nextDark)
      clearTransition(run)
    }
  }

  return { isDark: readonly(isDark), toggleTheme, applyTheme }
}
