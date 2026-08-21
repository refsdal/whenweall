import { customAlphabet, nanoid } from 'nanoid'

const ALPHABET = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz'

const pollIdAlphabet = customAlphabet(ALPHABET, 12)

export function newPollId(): string {
  return pollIdAlphabet()
}

export function newId(): string {
  return nanoid(16)
}
