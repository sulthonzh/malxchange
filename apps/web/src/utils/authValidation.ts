// Shared field validators for the auth pages. The sentences are the
// copy contract (ADR-0013): complete sentences, identical wherever the
// same rule fails, so the register and reset flows never disagree.

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function validateEmail(
  _rule: unknown,
  value: string,
  callback: (error?: Error) => void,
): void {
  if (value !== '' && !EMAIL_PATTERN.test(value)) {
    callback(new Error('Enter a valid email address.'))
    return
  }
  callback()
}

/** A confirm-password validator bound to the password field it checks. */
export function confirmPasswordMatches(password: () => string) {
  return (_rule: unknown, value: string, callback: (error?: Error) => void): void => {
    if (value === '') {
      callback(new Error('Enter your password again.'))
      return
    }
    if (value !== password()) {
      callback(new Error('Enter the same password twice.'))
      return
    }
    callback()
  }
}
