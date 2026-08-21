export const ERROR_CODES = [
  'UNAUTHORIZED',
  'FORBIDDEN',
  'NOT_FOUND',
  'POLL_CLOSED',
  'POLL_FINALIZED',
  'VALIDATION',
  'RATE_LIMITED',
  'CAPTCHA_FAILED',
  'EMAIL_REQUIRED',
  'LIMIT_REACHED',
  'INVALID_TOKEN',
  'CONFLICT',
] as const

export type ErrorCode = (typeof ERROR_CODES)[number]

export class AppError extends Error {
  constructor(
    public code: ErrorCode,
    message?: string,
  ) {
    super(message ?? code)
    this.name = 'AppError'
  }
}

export function errorCode(err: unknown): ErrorCode | null {
  if (err instanceof AppError) return err.code
  if (err instanceof Error && (ERROR_CODES as readonly string[]).includes(err.message)) {
    return err.message as ErrorCode
  }
  return null
}
