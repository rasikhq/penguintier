// Text rules for the Join window's network ID field. They are plain
// functions (no React), so `bun test` can check them: see networkId.test.ts.

/** A ZeroTier network ID is 16 hex digits, e.g. "8056c2e21c000001". */
export const NETWORK_ID_LENGTH = 16

/** Keeps only hex digits, lowercased, and at most 16 of them. */
export function filterHex(text: string): string {
    return text.toLowerCase().replace(/[^0-9a-f]/g, '').slice(0, NETWORK_ID_LENGTH)
}

/**
 * Cleans text pasted into the field. IDs copied from web pages and chats
 * often come with spaces or dashes ("8056c2e2-1c000001") or with words
 * around them ("Network ID: 8056c2e21c000001"). Spaces and dashes are
 * removed first; if that leaves a whole 16-digit ID, only the ID is kept.
 * Otherwise the hex digits are kept, so a partial paste still works.
 */
export function cleanPastedText(text: string): string {
    const compact = text.toLowerCase().replace(/[\s-]/g, '')
    const wholeId = compact.match(/[0-9a-f]{16}/)
    if (wholeId) {
        return wholeId[0]
    }
    return filterHex(compact)
}
