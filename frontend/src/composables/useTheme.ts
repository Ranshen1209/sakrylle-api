import { ref, readonly } from 'vue'

const isDark = ref(
  typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
)

let mediaQueryInitialized = false
let activeTransition: { finished: Promise<void> } | null = null
let transitionSafetyTimer: ReturnType<typeof setTimeout> | null = null

const TRANSITION_MS = 500

function syncBrowserChrome(dark: boolean) {
  // Do NOT set documentElement.style.colorScheme — CSS :root / :root.dark owns it.
  // Writing inline color-scheme during View Transitions corrupts painted snapshots.
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
  // The module is evaluated before main.ts restores the saved theme on refresh.
  // Read the painted DOM state at click time so the first toggle cannot use a
  // stale module-level ref and animate toward the theme that is already active.
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
  activeTransition = null
  document.documentElement.classList.remove('theme-toggling')
  document.documentElement.style.removeProperty('--theme-transition-bg')
}

function getTransitionOrigin(event?: MouseEvent) {
  const trigger = event?.currentTarget
  if (trigger instanceof HTMLElement) {
    const rect = trigger.getBoundingClientRect()
    if (rect.width > 0 && rect.height > 0) {
      return {
        x: Math.min(window.innerWidth, Math.max(0, rect.left + rect.width / 2)),
        y: Math.min(window.innerHeight, Math.max(0, rect.top + rect.height / 2))
      }
    }
  }

  // The first click after boot can arrive before the event target has a
  // usable layout box. Find the visible theme control instead of using a
  // browser-supplied zero coordinate in that case.
  const visibleTrigger = Array.from(document.querySelectorAll<HTMLElement>('[data-theme-toggle]')).find(
    (element) => {
      const rect = element.getBoundingClientRect()
      return rect.width > 0 && rect.height > 0
    }
  )
  if (visibleTrigger) {
    const rect = visibleTrigger.getBoundingClientRect()
    return {
      x: Math.min(window.innerWidth, Math.max(0, rect.left + rect.width / 2)),
      y: Math.min(window.innerHeight, Math.max(0, rect.top + rect.height / 2))
    }
  }

  return {
    x: event?.clientX || window.innerWidth / 2,
    y: event?.clientY || window.innerHeight / 2
  }
}

export function useTheme() {
  ensureSystemThemeListener()
  syncFromDom()

  function toggleTheme(event?: MouseEvent) {
    // Chromium can report a bogus position for the first click after a full
    // refresh. Anchor the reveal to the actual theme button instead.
    const { x, y } = getTransitionOrigin(event)
    const endRadius = Math.hypot(
      Math.max(x, window.innerWidth - x),
      Math.max(y, window.innerHeight - y)
    )

    const goingDark = !document.documentElement.classList.contains('dark')
    const supportsViewTransition = 'startViewTransition' in document

    if (
      !supportsViewTransition ||
      window.matchMedia('(prefers-reduced-motion: reduce)').matches ||
      activeTransition
    ) {
      document.documentElement.classList.add('theme-toggling')
      commitToggle()
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          document.documentElement.classList.remove('theme-toggling')
        })
      })
      return
    }

    // Destination page color under the reveal — prevents a 1-frame white/dark flash
    // between the DOM theme swap and the clip-path animation starting.
    document.documentElement.style.setProperty(
      '--theme-transition-bg',
      goingDark ? '#020617' : '#f9fafb'
    )
    document.documentElement.classList.add('theme-toggling')

    try {
      const transition = (
        document as Document & {
          startViewTransition: (cb: () => void) => {
            ready: Promise<void>
            finished: Promise<void>
          }
        }
      ).startViewTransition(() => {
        commitToggle()
      })

      activeTransition = transition
      transitionSafetyTimer = setTimeout(clearTransitionLock, TRANSITION_MS + 250)

      transition.ready
        .then(() => {
          // Always expand the NEW theme from the click point (both directions).
          // Shrinking the old layer on lighten races Chromium's default fade and flashes.
          document.documentElement.animate(
            {
              clipPath: [
                `circle(0px at ${x}px ${y}px)`,
                `circle(${endRadius}px at ${x}px ${y}px)`
              ]
            },
            {
              duration: TRANSITION_MS,
              easing: 'ease-in-out',
              // Apply the 0-radius keyframe immediately so the new layer never
              // paints full-bleed for a frame before the reveal starts.
              fill: 'both',
              pseudoElement: '::view-transition-new(root)'
            }
          )
        })
        .catch(() => {
          // Transition skipped; theme already applied in the callback.
        })

      transition.finished
        .catch(() => {})
        .finally(() => {
          document.documentElement.style.removeProperty('--theme-transition-bg')
          clearTransitionLock()
        })
    } catch {
      document.documentElement.style.removeProperty('--theme-transition-bg')
      clearTransitionLock()
      commitToggle()
    }
  }

  return { isDark: readonly(isDark), toggleTheme, applyTheme }
}
