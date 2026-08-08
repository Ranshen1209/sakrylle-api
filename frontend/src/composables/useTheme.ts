import { nextTick, ref, readonly } from 'vue'

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
  activeTransition = null
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

function getPointerOrigin(event?: MouseEvent): TransitionOrigin | null {
  if (!event || !Number.isFinite(event.clientX) || !Number.isFinite(event.clientY)) return null
  if (
    event.clientX < 0 ||
    event.clientX > window.innerWidth ||
    event.clientY < 0 ||
    event.clientY > window.innerHeight
  ) {
    return null
  }
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

function waitForNextPaint() {
  return new Promise<void>((resolve) => {
    if (typeof window.requestAnimationFrame === 'function') {
      window.requestAnimationFrame(() => resolve())
    } else {
      resolve()
    }
  })
}

async function getTransitionOrigin(event?: MouseEvent, trigger?: EventTarget | null): Promise<TransitionOrigin> {
  // On mobile widths the sidebar can still be finishing its transform entry
  // transition when the first control click arrives after refresh. Let that
  // frame settle before reading its geometry.
  await waitForNextPaint()
  const pointer = getPointerOrigin(event)
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

  async function toggleTheme(event?: MouseEvent) {
    // Event.currentTarget is cleared by the browser once an async handler
    // yields, so retain the clicked button before waiting for layout to settle.
    const trigger = event?.currentTarget
    // Chromium can report a bogus position for the first click after a full
    // refresh. Anchor the reveal to the actual theme button instead.
    const { x, y } = await getTransitionOrigin(event, trigger)
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
          startViewTransition: (cb: () => Promise<void>) => {
            ready: Promise<void>
            finished: Promise<void>
          }
        }
      ).startViewTransition(async () => {
        commitToggle()
        // View Transitions waits for this promise before capturing the new
        // snapshot. Flush theme-driven icons, labels, charts, and layout first.
        await nextTick()
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
