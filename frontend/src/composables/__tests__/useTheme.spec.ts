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
  const startViewTransition = vi.fn()
  Object.defineProperty(document, 'startViewTransition', {
    configurable: true,
    value: startViewTransition
  })
  return { animate, finished, finishAnimation, startViewTransition }
}

function installQueuedAnimationMock() {
  const finishAnimations: Array<() => void> = []
  const animate = vi.fn().mockImplementation(() => ({
    finished: new Promise<void>((resolve) => finishAnimations.push(resolve))
  }))
  Object.defineProperty(HTMLElement.prototype, 'animate', {
    configurable: true,
    value: animate
  })
  return { animate, finishAnimations }
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

  it('uses the exact pointer position for the reveal origin', async () => {
    const { animate, finished, finishAnimation, startViewTransition } = installAnimationMock()
    const { useTheme } = await import('../useTheme')
    document.documentElement.classList.add('dark')

    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)
    button.dispatchEvent(new MouseEvent('click', { clientX: 38, clientY: 500 }))

    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBeNull()
    const radius = Math.hypot(
      Math.max(38, window.innerWidth - 38),
      Math.max(500, window.innerHeight - 500)
    )
    const ripple = document.querySelector<HTMLElement>('[data-theme-ripple]')
    expect(ripple?.dataset.themeRippleX).toBe('38')
    expect(ripple?.dataset.themeRippleY).toBe('500')
    expect(animate).toHaveBeenCalledWith(
      [
        { clipPath: 'circle(0px at 38px 500px)', opacity: 0.34 },
        {
          clipPath: `circle(${radius * 0.82}px at 38px 500px)`,
          opacity: 0.12,
          offset: 0.72
        },
        { clipPath: `circle(${radius}px at 38px 500px)`, opacity: 0 }
      ],
      expect.objectContaining({
        duration: 420
      })
    )
    expect(startViewTransition).not.toHaveBeenCalled()

    finishAnimation()
    await finished
    await vi.waitFor(() => {
      expect(document.documentElement.classList.contains('theme-toggling')).toBe(false)
    })
    expect(document.querySelector('[data-theme-ripple]')).toBeNull()
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
    const ripple = document.querySelector<HTMLElement>('[data-theme-ripple]')
    expect(ripple?.dataset.themeRippleX).toBe('580')
    expect(ripple?.dataset.themeRippleY).toBe('40')
    expect(animate).toHaveBeenCalledWith(
      expect.arrayContaining([
        expect.objectContaining({ clipPath: `circle(${radius}px at 580px 40px)` })
      ]),
      expect.any(Object)
    )
    expect(animate).toHaveBeenCalledTimes(1)
    finishAnimation()
  })

  it('coalesces rapid repeated toggles without root snapshots', async () => {
    const { animate, finished, finishAnimation, startViewTransition } = installAnimationMock()
    const { useTheme } = await import('../useTheme')

    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)
    for (let index = 0; index < 50; index += 1) {
      button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    }

    expect(animate).toHaveBeenCalledTimes(1)
    expect(startViewTransition).not.toHaveBeenCalled()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.querySelectorAll('[data-theme-ripple]')).toHaveLength(1)
    expect(localStorage.getItem('theme')).toBeNull()

    finishAnimation()
    await finished
    await vi.waitFor(() => {
      expect(document.documentElement.classList.contains('theme-toggling')).toBe(false)
    })
    button.dispatchEvent(new MouseEvent('click'))
    expect(animate).toHaveBeenCalledTimes(2)
    expect(localStorage.getItem('theme')).toBeNull()
  })

  it('does not let stale animation cleanup unlock a newer reveal', async () => {
    vi.useFakeTimers()
    const { animate, finishAnimations } = installQueuedAnimationMock()
    const { useTheme } = await import('../useTheme')
    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)

    button.dispatchEvent(new MouseEvent('click'))
    expect(animate).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(700)

    button.dispatchEvent(new MouseEvent('click'))
    expect(animate).toHaveBeenCalledTimes(2)
    finishAnimations[0]?.()
    await Promise.resolve()

    expect(document.documentElement.classList.contains('theme-toggling')).toBe(true)
    expect(document.querySelectorAll('[data-theme-ripple]')).toHaveLength(1)

    finishAnimations[1]?.()
    await Promise.resolve()
    await Promise.resolve()
    expect(document.documentElement.classList.contains('theme-toggling')).toBe(false)
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
