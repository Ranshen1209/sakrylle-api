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
    const button = document.createElement('button')
    vi.spyOn(button, 'getBoundingClientRect').mockReturnValue({
      left: 24,
      top: 480,
      width: 200,
      height: 48,
      right: 224,
      bottom: 528,
      x: 24,
      y: 480,
      toJSON: () => ({})
    })
    button.addEventListener('click', toggleTheme)
    button.dispatchEvent(new MouseEvent('click', { clientX: 0, clientY: 0 }))
    await Promise.resolve()

    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(isDark.value).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
    expect(document.documentElement.style.getPropertyValue('--theme-transition-bg')).toBe('#f9fafb')
    expect(animate).toHaveBeenCalledWith(
      expect.objectContaining({
        clipPath: [
          'circle(0px at 124px 504px)',
          expect.stringMatching(/^circle\(.+px at 124px 504px\)$/)
        ]
      }),
      expect.objectContaining({ pseudoElement: '::view-transition-new(root)' })
    )

    finishTransition()
    await finished
  })

  it('uses the visible theme control when the click target has no layout box', async () => {
    const { useTheme } = await import('../useTheme')
    document.documentElement.classList.add('dark')

    const animate = vi.fn()
    Object.defineProperty(document.documentElement, 'animate', { configurable: true, value: animate })
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

    const visibleButton = document.createElement('button')
    visibleButton.dataset.themeToggle = ''
    vi.spyOn(visibleButton, 'getBoundingClientRect').mockReturnValue({
      left: 24,
      top: 480,
      width: 200,
      height: 48,
      right: 224,
      bottom: 528,
      x: 24,
      y: 480,
      toJSON: () => ({})
    })
    document.body.appendChild(visibleButton)

    const hiddenButton = document.createElement('button')
    vi.spyOn(hiddenButton, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 0,
      height: 0,
      right: 0,
      bottom: 0,
      x: 0,
      y: 0,
      toJSON: () => ({})
    })
    hiddenButton.addEventListener('click', useTheme().toggleTheme)
    hiddenButton.dispatchEvent(new MouseEvent('click', { clientX: 0, clientY: 0 }))
    await Promise.resolve()

    expect(animate).toHaveBeenCalledWith(
      expect.objectContaining({
        clipPath: [
          'circle(0px at 124px 504px)',
          expect.stringMatching(/^circle\(.+px at 124px 504px\)$/)
        ]
      }),
      expect.objectContaining({ pseudoElement: '::view-transition-new(root)' })
    )
    finishTransition()
    await finished
  })
})
