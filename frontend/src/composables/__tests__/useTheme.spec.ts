import { beforeEach, describe, expect, it, vi } from 'vitest'

function installMatchMedia() {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn()
    }))
  })
}

describe('useTheme', () => {
  beforeEach(() => {
    vi.resetModules()
    localStorage.clear()
    document.documentElement.className = ''
    document.documentElement.style.removeProperty('--theme-transition-bg')
    installMatchMedia()
  })

  it('uses the restored DOM theme for the first toggle after refresh', async () => {
    const { useTheme } = await import('../useTheme')

    // main.ts restores the saved class after the composable module is evaluated.
    document.documentElement.classList.add('dark')

    const animate = vi.fn()
    Object.defineProperty(document.documentElement, 'animate', {
      configurable: true,
      value: animate
    })

    let finishTransition: () => void = () => {}
    const finished = new Promise<void>((resolve) => {
      finishTransition = resolve
    })
    Object.defineProperty(document, 'startViewTransition', {
      configurable: true,
      value: vi.fn((callback: () => void) => {
        callback()
        return { ready: Promise.resolve(), finished }
      })
    })

    const { toggleTheme, isDark } = useTheme()
    toggleTheme(new MouseEvent('click', { clientX: 40, clientY: 60 }))
    await Promise.resolve()

    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(isDark.value).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
    expect(document.documentElement.style.getPropertyValue('--theme-transition-bg')).toBe('#f9fafb')
    expect(animate).toHaveBeenCalledWith(
      expect.objectContaining({ clipPath: expect.any(Array) }),
      expect.objectContaining({ pseudoElement: '::view-transition-new(root)' })
    )

    finishTransition()
    await finished
  })
})
