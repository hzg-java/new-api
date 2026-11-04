import { render, screen } from '@testing-library/react'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'

import { ImageDialog } from '../dialogs/image-dialog'

const originalGetAnimations = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)

beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})

afterAll(() => {
  if (originalGetAnimations) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      originalGetAnimations
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
  }
})

describe('generated image dialog compatibility', () => {
  it.each([
    { taskId: undefined, description: 'View the generated image' },
    { taskId: 'task-image', description: 'Task ID: task-image' },
  ])(
    'preserves the default description and URL for task $taskId',
    ({ taskId, description }) => {
      const imageUrl = 'https://example.com/generated.png'
      render(
        <ImageDialog
          imageUrl={imageUrl}
          taskId={taskId}
          open
          onOpenChange={vi.fn()}
        />
      )
      expect(
        screen.getByRole('dialog', { name: 'Image Preview' })
      ).toHaveAccessibleDescription(description)
      expect(
        screen.getByRole('img', { name: 'Generated image' })
      ).toHaveAttribute('src', imageUrl)
      expect(screen.getByText(imageUrl)).toBeVisible()
    }
  )
})
