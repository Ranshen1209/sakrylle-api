import { describe, expect, it } from 'vitest'
import { getAdminSteps, getUserSteps } from '../steps'

const emojiTranslation = (key: string) => `👋 ${key} 🚀`
const decorativeEmoji = /\p{Extended_Pictographic}/u

describe('onboarding steps', () => {
  it.each([
    ['admin', () => getAdminSteps(emojiTranslation)],
    ['admin simple mode', () => getAdminSteps(emojiTranslation, true)],
    ['user', () => getUserSteps(emojiTranslation)]
  ])('removes decorative emoji from the %s tour', (_name, createSteps) => {
    expect(JSON.stringify(createSteps())).not.toMatch(decorativeEmoji)
  })
})
