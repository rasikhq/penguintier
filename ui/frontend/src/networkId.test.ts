// Run with `bun test` (wails build runs it too, see ui/wails.json).
import {describe, expect, test} from 'bun:test'
import {cleanPastedText, filterHex} from './networkId'

describe('filterHex', () => {
    test.each([
        ['8056c2e21c000001', '8056c2e21c000001'],
        ['8056C2E21C000001', '8056c2e21c000001'],
        ['80 56-c2:e2', '8056c2e2'],
        ['xyz', ''],
        ['8056c2e21c00000123', '8056c2e21c000001'], // at most 16
    ])('%p -> %p', (input, want) => {
        expect(filterHex(input)).toBe(want)
    })
})

describe('cleanPastedText', () => {
    test.each([
        ['8056c2e21c000001', '8056c2e21c000001'],
        ['  8056C2E21C000001\n', '8056c2e21c000001'],
        ['8056c2e2-1c000001', '8056c2e21c000001'],
        ['8056 c2e2 1c00 0001', '8056c2e21c000001'],
        ['Network ID: 8056c2e21c000001', '8056c2e21c000001'],
        ['c2e2', 'c2e2'], // part of an ID
        ['zz 12-ab', '12ab'],
    ])('%p -> %p', (input, want) => {
        expect(cleanPastedText(input)).toBe(want)
    })
})
