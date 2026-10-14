// Shared form rules (issue #63), following setup/password.vue's
// pattern: refusals are enforced by custom validator callbacks. The
// element-plus `required` shortcut is deliberately avoided — its
// rejection shape has proven fragile under test, and the callback
// form is the codebase's one convention.
export interface ElFormRule {
  validator?: (rule: unknown, value: unknown, callback: (error?: Error) => void) => void
  trigger?: string
  message?: string
  min?: number
  max?: number
}

type Callback = (error?: Error) => void

/** Fails on a blank (or whitespace-only) value. */
export function requiredRule(message: string): ElFormRule {
  return {
    validator: (_rule: unknown, value: unknown, callback: Callback): void => {
      if (String(value ?? '').trim() === '') {
        callback(new Error(message))
        return
      }
      callback()
    },
    trigger: 'blur',
  }
}
