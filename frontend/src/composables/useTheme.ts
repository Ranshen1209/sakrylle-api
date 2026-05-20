import { ref, onMounted, onUnmounted } from 'vue'

const isDark = ref(false)

function applyTheme(dark: boolean) {
  isDark.value = dark
  document.documentElement.classList.toggle('dark', dark)
}

export function useTheme() {
  let mediaQuery: MediaQueryList | null = null
  let mediaHandler: ((e: MediaQueryListEvent) => void) | null = null

  function toggleTheme(event?: MouseEvent) {
    const x = event?.clientX ?? window.innerWidth / 2
    const y = event?.clientY ?? window.innerHeight / 2
    const endRadius = Math.hypot(
      Math.max(x, window.innerWidth - x),
      Math.max(y, window.innerHeight - y)
    )

    const supportsViewTransition = 'startViewTransition' in document
    if (!supportsViewTransition || window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      commitToggle()
      return
    }

    const transition = (document as any).startViewTransition(() => {
      commitToggle()
    })

    transition.ready.then(() => {
      document.documentElement.animate(
        { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${endRadius}px at ${x}px ${y}px)`] },
        { duration: 500, easing: 'ease-in-out', pseudoElement: '::view-transition-new(root)' }
      )
    })
  }

  function commitToggle() {
    const newDark = !isDark.value
    applyTheme(newDark)
    localStorage.setItem('theme', newDark ? 'dark' : 'light')
  }

  function initTheme() {
    const savedTheme = localStorage.getItem('theme')
    if (savedTheme === 'dark') {
      applyTheme(true)
    } else if (savedTheme === 'light') {
      applyTheme(false)
    } else {
      applyTheme(window.matchMedia('(prefers-color-scheme: dark)').matches)
    }

    mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
    mediaHandler = (e: MediaQueryListEvent) => {
      if (!localStorage.getItem('theme')) {
        applyTheme(e.matches)
      }
    }
    mediaQuery.addEventListener('change', mediaHandler)
  }

  function cleanupTheme() {
    if (mediaQuery && mediaHandler) {
      mediaQuery.removeEventListener('change', mediaHandler)
    }
  }

  onMounted(initTheme)
  onUnmounted(cleanupTheme)

  return { isDark, toggleTheme, initTheme }
}
