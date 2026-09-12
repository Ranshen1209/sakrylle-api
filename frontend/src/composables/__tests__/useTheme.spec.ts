import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

type Deferred = {
  promise: Promise<void>
  resolve: () => void
  reject: (reason?: unknown) => void
}

function deferred(): Deferred {
  let resolve: () => void = () => {}
  let reject: (reason?: unknown) => void = () => {}
  const promise = new Promise<void>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

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

function installViewTransitionMock() {
  const runs: Array<{
    ready: Deferred
    finished: Deferred
    updateCallbackDone: Deferred
    skipTransition: ReturnType<typeof vi.fn>
  }> = []
  const animate = vi.fn()
  Object.defineProperty(document.documentElement, 'animate', {
    configurable: true,
    value: animate
  })

  const startViewTransition = vi.fn().mockImplementation((update: () => void | Promise<void>) => {
    const ready = deferred()
    const finished = deferred()
    const updateCallbackDone = deferred()
    const skipTransition = vi.fn()

    try {
      Promise.resolve(update()).then(updateCallbackDone.resolve, updateCallbackDone.reject)
    } catch (error) {
      updateCallbackDone.reject(error)
    }

    runs.push({ ready, finished, updateCallbackDone, skipTransition })
    return {
      ready: ready.promise,
      finished: finished.promise,
      updateCallbackDone: updateCallbackDone.promise,
      skipTransition
    }
  })
  Object.defineProperty(document, 'startViewTransition', {
    configurable: true,
    value: startViewTransition
  })

  return { animate, runs, startViewTransition }
}

async function flushPromises() {
  await Promise.resolve()
  await Promise.resolve()
  await Promise.resolve()
}

async function finishNativeTransition(run: { finished: Deferred }) {
  run.finished.resolve()
  await flushPromises()
  await vi.runAllTimersAsync()
}

function readRevealGeometry() {
  const percent = (name: string) => {
    const value = document.documentElement.style.getPropertyValue(`--theme-transition-${name}`)
    expect(value.endsWith('%')).toBe(true)
    return parseFloat(value) / 100
  }
  return {
    x: percent('x') * window.innerWidth,
    y: percent('y') * window.innerHeight,
    radius: percent('radius') * Math.sqrt((window.innerWidth ** 2 + window.innerHeight ** 2) / 2)
  }
}

function expectRevealOrigin(x: number, y: number) {
  const geometry = readRevealGeometry()
  expect(geometry.x).toBeCloseTo(x)
  expect(geometry.y).toBeCloseTo(y)
}

describe('useTheme', () => {
  afterEach(() => vi.unstubAllGlobals())

  beforeEach(() => {
    vi.resetModules()
    vi.useFakeTimers()
    localStorage.clear()
    document.body.innerHTML = ''
    document.documentElement.className = ''
    document.documentElement.removeAttribute('style')
    Object.defineProperty(document, 'startViewTransition', {
      configurable: true,
      value: undefined
    })
    Object.defineProperty(document.documentElement, 'animate', {
      configurable: true,
      value: undefined
    })
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
      window.setTimeout(() => callback(0), 0)
    )
    installMatchMedia()
  })

  it('reveals the destination theme from the exact pointer position', async () => {
    const mock = installViewTransitionMock()
    const { useTheme } = await import('../useTheme')
    document.documentElement.classList.add('dark')

    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)
    button.dispatchEvent(new MouseEvent('click', { clientX: 38, clientY: 500 }))

    expect(mock.startViewTransition).toHaveBeenCalledTimes(1)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expectRevealOrigin(38, 500)
    expect(localStorage.getItem('theme')).toBeNull()

    const run = mock.runs[0]!
    run.ready.resolve()
    await flushPromises()

    const radius = Math.hypot(
      Math.max(38, window.innerWidth - 38),
      Math.max(500, window.innerHeight - 500)
    )
    expect(readRevealGeometry().radius).toBeCloseTo(radius)
    // The native snapshot owns its CSS animation; no filled root effect remains.
    expect(mock.animate).not.toHaveBeenCalled()

    await flushPromises()
    button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 501 }))
    expect(mock.startViewTransition).toHaveBeenCalledTimes(1)
    expect(document.documentElement.classList.contains('theme-toggling')).toBe(true)

    await finishNativeTransition(run)
    expect(document.documentElement.classList.contains('theme-toggling')).toBe(false)
    expect(document.documentElement.style.getPropertyValue('--theme-transition-x')).toBe('')
  })

  it('uses a visible control center for keyboard or off-screen clicks', async () => {
    const mock = installViewTransitionMock()
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

    const run = mock.runs[0]!
    run.ready.resolve()
    await flushPromises()

    expectRevealOrigin(580, 40)
    expect(mock.animate).not.toHaveBeenCalled()
    await finishNativeTransition(run)
  })

  it('keeps the first pointer position when the control layout is still moving', async () => {
    const mock = installViewTransitionMock()
    const { useTheme } = await import('../useTheme')
    const button = document.createElement('button')
    mockRect(button, 24, 240, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)
    button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 700 }))

    expectRevealOrigin(40, 700)
    await finishNativeTransition(mock.runs[0]!)
    expect(document.documentElement.style.getPropertyValue('--theme-transition-radius')).toBe('')
  })

  it.each([
    { width: 1380, height: 972, dpr: 1, x: 117, y: 895 },
    { width: 1380, height: 972, dpr: 2, x: 117, y: 895 },
    { width: 390, height: 844, dpr: 3, x: 355, y: 24 }
  ])('keeps the origin and covers all corners at $width x $height, DPR $dpr', async ({ width, height, dpr, x, y }) => {
    vi.stubGlobal('innerWidth', width)
    vi.stubGlobal('innerHeight', height)
    vi.stubGlobal('devicePixelRatio', dpr)
    const mock = installViewTransitionMock()
    const { useTheme } = await import('../useTheme')

    useTheme().toggleTheme(new MouseEvent('click', { clientX: x, clientY: y }))
    expectRevealOrigin(x, y)
    const { radius } = readRevealGeometry()
    for (const [cornerX, cornerY] of [[0, 0], [width, 0], [0, height], [width, height]]) {
      expect(Math.hypot(cornerX! - x, cornerY! - y)).toBeLessThanOrEqual(radius + 1e-8)
    }
    await finishNativeTransition(mock.runs[0]!)
  })

  it('coalesces 50 clicks until the native transition and compositor cooldown finish', async () => {
    const mock = installViewTransitionMock()
    const { useTheme } = await import('../useTheme')

    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)
    for (let index = 0; index < 50; index += 1) {
      button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    }

    const run = mock.runs[0]!
    expect(mock.startViewTransition).toHaveBeenCalledTimes(1)
    expect(document.documentElement.classList.contains('dark')).toBe(true)

    run.ready.resolve()
    await flushPromises()
    await flushPromises()
    for (let index = 0; index < 50; index += 1) {
      button.dispatchEvent(new MouseEvent('click', { clientX: 42, clientY: 501 }))
    }
    expect(mock.startViewTransition).toHaveBeenCalledTimes(1)
    expect(document.documentElement.classList.contains('dark')).toBe(true)

    run.finished.resolve()
    await flushPromises()
    button.dispatchEvent(new MouseEvent('click'))
    expect(mock.startViewTransition).toHaveBeenCalledTimes(1)

    await vi.runAllTimersAsync()
    button.dispatchEvent(new MouseEvent('click', { clientX: 42, clientY: 501 }))
    expect(mock.startViewTransition).toHaveBeenCalledTimes(2)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('uses the watchdog only to skip a stuck transition and never unlocks it early', async () => {
    const mock = installViewTransitionMock()
    const { useTheme } = await import('../useTheme')
    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)

    button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    const run = mock.runs[0]!
    await vi.advanceTimersByTimeAsync(1999)
    expect(run.skipTransition).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(run.skipTransition).toHaveBeenCalledTimes(1)

    for (let index = 0; index < 50; index += 1) {
      button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    }
    expect(mock.startViewTransition).toHaveBeenCalledTimes(1)
    expect(document.documentElement.classList.contains('theme-toggling')).toBe(true)

    await finishNativeTransition(run)
    button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    expect(mock.startViewTransition).toHaveBeenCalledTimes(1)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('keeps the lock through ready failures and disables later native snapshots', async () => {
    const mock = installViewTransitionMock()
    const { useTheme } = await import('../useTheme')
    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)

    button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    const run = mock.runs[0]!
    run.ready.reject(new Error('snapshot failed'))
    await flushPromises()
    expect(run.skipTransition).toHaveBeenCalledTimes(1)

    button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    expect(mock.startViewTransition).toHaveBeenCalledTimes(1)
    await finishNativeTransition(run)

    button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    expect(mock.startViewTransition).toHaveBeenCalledTimes(1)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('queues browser color-scheme changes until the reveal finishes', async () => {
    const media = installMatchMedia()
    const mock = installViewTransitionMock()
    const { useTheme } = await import('../useTheme')
    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)

    button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    media.setColorScheme(false)
    expect(document.documentElement.classList.contains('dark')).toBe(true)

    await finishNativeTransition(mock.runs[0]!)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('keeps following live browser color-scheme changes after a manual toggle', async () => {
    const media = installMatchMedia()
    const { useTheme } = await import('../useTheme')
    const { isDark, toggleTheme } = useTheme()

    media.setColorScheme(true)
    expect(isDark.value).toBe(true)

    toggleTheme()
    expect(isDark.value).toBe(false)
    expect(localStorage.getItem('theme')).toBeNull()

    media.setColorScheme(true)
    expect(isDark.value).toBe(true)
  })

  it('initializes browser theme syncing without requiring a rendered toggle', async () => {
    const media = installMatchMedia()
    const { initializeThemeSync } = await import('../useTheme')

    initializeThemeSync()
    media.setColorScheme(true)

    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('ignores a stale ready callback after a newer transition starts', async () => {
    const mock = installViewTransitionMock()
    const { useTheme } = await import('../useTheme')
    const button = document.createElement('button')
    mockRect(button, 24, 480, 200, 48)
    button.addEventListener('click', useTheme().toggleTheme)

    button.dispatchEvent(new MouseEvent('click', { clientX: 40, clientY: 500 }))
    const firstRun = mock.runs[0]!
    await finishNativeTransition(firstRun)

    button.dispatchEvent(new MouseEvent('click', { clientX: 42, clientY: 501 }))
    expect(mock.startViewTransition).toHaveBeenCalledTimes(2)
    firstRun.ready.resolve()
    await flushPromises()

    expect(mock.animate).not.toHaveBeenCalled()
    await finishNativeTransition(mock.runs[1]!)
  })

  it('falls back once and disables native transitions after a synchronous API failure', async () => {
    const animate = vi.fn()
    Object.defineProperty(document.documentElement, 'animate', {
      configurable: true,
      value: animate
    })
    const startViewTransition = vi.fn(() => {
      throw new Error('view transition unavailable')
    })
    Object.defineProperty(document, 'startViewTransition', {
      configurable: true,
      value: startViewTransition
    })
    const { useTheme } = await import('../useTheme')

    const { toggleTheme } = useTheme()
    toggleTheme()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.classList.contains('theme-toggling')).toBe(false)

    toggleTheme()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(startViewTransition).toHaveBeenCalledTimes(1)
    expect(animate).not.toHaveBeenCalled()
  })

  it('switches immediately when reduced motion is requested', async () => {
    installMatchMedia(true)
    const mock = installViewTransitionMock()
    const { useTheme } = await import('../useTheme')

    useTheme().toggleTheme()

    expect(mock.startViewTransition).not.toHaveBeenCalled()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('theme')).toBeNull()
  })
})
