import { beforeEach, describe, expect, it, vi } from 'vitest'

function installMatchMedia(reducedMotion = false) {
  let colorSchemeListener: ((event: MediaQueryListEvent) => void) | undefined
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: reducedMotion && query === '(prefers-reduced-motion: reduce)',
      media: query,
      addEventListener: vi.fn((_type: string, listener: (event: MediaQueryListEvent) => void) => {
        if (query === '(prefers-color-scheme: dark)') colorSchemeListener = listener
      }),
      removeEventListener: vi.fn()
    }))
  })
  return {
    setColorScheme(dark: boolean) {
      colorSchemeListener?.({ matches: dark } as MediaQueryListEvent)
    }
  }
}

function mockRect(element: HTMLElement, left: number, top: number, width: number, height: number) {
  vi.spyOn(element, 'getBoundingClientRect').mockReturnValue({
    left,
    top,
    width,
    height,
    right: left + width,
    bottom: top + height,
    x: left,
    y: top,
    toJSON: () => ({})
  })
}

function installAnimationMock() {
  let finishAnimation: () => void = () => {}
  const finished = new Promise<void>((resolve) => {
    finishAnimation = resolve
  })
  const animate = vi.fn().mockReturnValue({ finished })
  Object.defineProperty(HTMLElement.prototype, 'animate', {
    configurable: true,
    value: animate
  })
  const startViewTransition = vi.fn((update: () => void) => {
    update()
    return { ready: Promise.resolve() }
  })
  Object.defineProperty(document, 'startViewTransition', {
    configurable: true,
    value: startViewTransition
  })
  return { animate, finished, finishAnimation, startViewTransition }
}

describe('useTheme', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.useRealTimers()
    localStorage.clear()
    document.body.innerHTML = ''
    document.documentElement.className = ''
    installMatchMedia()
  })

  it('uses the restored DOM theme and the button center for the first toggle', async () => {
    const { animate, finished, finishAnimation } = installAnimationMock()
    const { useTheme } = await import('../useTheme')
    document.documentElement.classList.add('dark')

    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)
    button.dispatchEvent(new MouseEvent('click', { clientX: 0, clientY: 0 }))

    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBeNull()
    const radius = Math.hypot(Math.max(124, window.innerWidth - 124), Math.max(504, window.innerHeight - 504))
    await Promise.resolve()
    expect(animate).toHaveBeenCalledWith(
      {
        clipPath: [
          'circle(0px at 124px 504px)',
          `circle(${radius}px at 124px 504px)`
        ]
      },
      expect.objectContaining({
        duration: 500,
        pseudoElement: '::view-transition-new(root)'
      })
    )

    finishAnimation()
    await finished
    await vi.waitFor(() => {
      expect(document.documentElement.classList.contains('theme-toggling')).toBe(false)
    })
  })

  it('uses a visible theme control when the clicked control is off-screen', async () => {
    const { animate, finishAnimation } = installAnimationMock()
    const { useTheme } = await import('../useTheme')

    const visibleButton = document.createElement('button')
    visibleButton.dataset.themeToggle = ''
    mockRect(visibleButton, 560, 20, 40, 40)
    document.body.appendChild(visibleButton)

    const offscreenButton = document.createElement('button')
    offscreenButton.dataset.themeToggle = ''
    mockRect(offscreenButton, -244, 480, 231, 40)
    offscreenButton.addEventListener('click', useTheme().toggleTheme)
    offscreenButton.dispatchEvent(new MouseEvent('click'))

    const radius = Math.hypot(Math.max(580, window.innerWidth - 580), Math.max(40, window.innerHeight - 40))
    await Promise.resolve()
    expect(animate).toHaveBeenCalledWith(
      {
        clipPath: [
          'circle(0px at 580px 40px)',
          `circle(${radius}px at 580px 40px)`
        ]
      },
      expect.objectContaining({ pseudoElement: '::view-transition-new(root)' })
    )
    expect(animate).toHaveBeenCalledTimes(1)
    finishAnimation()
  })

  it('ignores rapid repeated toggles until the current reveal finishes', async () => {
    const { animate, finished, finishAnimation } = installAnimationMock()
    const { useTheme } = await import('../useTheme')

    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)
    button.dispatchEvent(new MouseEvent('click'))
    button.dispatchEvent(new MouseEvent('click'))
    button.dispatchEvent(new MouseEvent('click'))

    await Promise.resolve()
    expect(animate).toHaveBeenCalledTimes(1)
    expect(localStorage.getItem('theme')).toBeNull()

    finishAnimation()
    await finished
    await vi.waitFor(() => {
      expect(document.documentElement.classList.contains('theme-toggling')).toBe(false)
    })
    button.dispatchEvent(new MouseEvent('click'))
    await Promise.resolve()
    expect(animate).toHaveBeenCalledTimes(2)
    expect(localStorage.getItem('theme')).toBeNull()
  })

  it('follows browser color-scheme changes after a manual toggle', async () => {
    const media = installMatchMedia(true)
    const { useTheme } = await import('../useTheme')
    const { toggleTheme, isDark } = useTheme()

    toggleTheme()
    expect(isDark.value).toBe(true)

    media.setColorScheme(false)
    expect(isDark.value).toBe(false)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
