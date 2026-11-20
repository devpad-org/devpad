/** The fewest characters a password may have (auth.MinPasswordLength). */
export const MIN_PASSWORD_CHARS = 8
/** The most bytes a password may have, bcrypt's limit (auth.MaxPasswordLength). */
export const MAX_PASSWORD_BYTES = 72

/**
 * What is wrong with a password, or null if nothing is, matching the server's
 * auth.ValidatePassword: the minimum counts characters (code points, not
 * UTF-16 units), the maximum counts UTF-8 bytes.
 */
export function passwordProblem(password: string): string | null {
  if ([...password].length < MIN_PASSWORD_CHARS) {
    return `Password must be at least ${MIN_PASSWORD_CHARS} characters`
  }
  if (new TextEncoder().encode(password).length > MAX_PASSWORD_BYTES) {
    return `Password must be at most ${MAX_PASSWORD_BYTES} bytes (letters outside plain ASCII take 2 to 4 bytes each)`
  }
  return null
}
