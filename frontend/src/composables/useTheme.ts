import { ref, readonly } from 'vue'

const isDark = ref(
  typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
)

let mediaQueryInitialized = false
let activeTransition: { finished: Promise<void> } | null = null

function syncBrowserChrome(dark: boolean) {
  // Do NOT set documentElement.style.colorScheme here.
  // Inline color-scheme during View Transitions corrupts the painted snapshots
  // (DOM ends up light while form controls / overlays still look dark, or vice versa).
  // CSS already sets color-scheme on :root / :root.dark.
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
  const next = !isDark.value
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

/**
 * Sync the shared ref from the current DOM class (main.ts may have applied it already).
 */
function syncFromDom() {
  if (typeof document === 'undefined') return
  isDark.value = document.documentElement.classList.contains('dark')
}

export function useTheme() {
  ensureSystemThemeListener()
  syncFromDom()

  function toggleTheme(event?: MouseEvent) {
    const x = event?.clientX ?? window.innerWidth / 2
    const y = event?.clientY ?? window.innerHeight / 2
    const endRadius = Math.hypot(
      Math.max(x, window.innerWidth - x),
      Math.max(y, window.innerHeight - y)
    )

    const supportsViewTransition = 'startViewTransition' in document
    if (
      !supportsViewTransition ||
      window.matchMedia('(prefers-reduced-motion: reduce)').matches ||
      activeTransition
    ) {
      commitToggle()
      return
    }

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

      transition.ready
        .then(() => {
          document.documentElement.animate(
            {
              clipPath: [
                `circle(0px at ${x}px ${y}px)`,
                `circle(${endRadius}px at ${x}px ${y}px)`
              ]
            },
            {
              duration: 500,
              easing: 'ease-in-out',
              pseudoElement: '::view-transition-new(root)'
            }
          )
        })
        .catch(() => {
          // Transition was skipped; theme already applied in the callback.
        })

      transition.finished
        .catch(() => {})
        .finally(() => {
          activeTransition = null
          document.documentElement.classList.remove('theme-toggling')
        })
    } catch {
      activeTransition = null
      document.documentElement.classList.remove('theme-toggling')
      commitToggle()
    }
  }

  return { isDark: readonly(isDark), toggleTheme, applyTheme }
}
