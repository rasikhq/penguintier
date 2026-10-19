import {ChangeEvent, ClipboardEvent, FormEvent, useEffect, useState} from 'react'
import {Cancel, Join as JoinNetwork} from '../wailsjs/go/main/App'
import {NETWORK_ID_LENGTH, cleanPastedText, filterHex} from './networkId'

// The Join Network window. The Go side checks the ID again, prints it on
// stdout and exits 0; Cancel, Esc or closing the window exits 1.
export default function Join() {
    const [networkId, setNetworkId] = useState('')
    const [error, setError] = useState('')
    const [joining, setJoining] = useState(false)
    const complete = networkId.length === NETWORK_ID_LENGTH

    // Esc cancels from anywhere in the window.
    useEffect(() => {
        function onKeyDown(event: KeyboardEvent) {
            if (event.key === 'Escape') {
                event.preventDefault()
                Cancel()
            }
        }
        window.addEventListener('keydown', onKeyDown)
        return () => window.removeEventListener('keydown', onKeyDown)
    }, [])

    // Filters the field's text. The filtered text and the caret go straight
    // into the field (the caret moves back by any characters dropped before
    // it); React then finds the field already up to date, so it doesn't
    // reset it and throw the caret to the end.
    function update(input: HTMLInputElement, caret: number) {
        const keptBeforeCaret = filterHex(input.value.slice(0, caret)).length
        const filtered = filterHex(input.value)
        input.value = filtered
        input.setSelectionRange(keptBeforeCaret, keptBeforeCaret)
        setNetworkId(filtered)
        setError('')
    }

    function onChange(event: ChangeEvent<HTMLInputElement>) {
        const input = event.currentTarget
        update(input, input.selectionStart ?? input.value.length)
    }

    function onPaste(event: ClipboardEvent<HTMLInputElement>) {
        event.preventDefault()
        const input = event.currentTarget
        const pasted = cleanPastedText(event.clipboardData.getData('text'))
        if (pasted.length === NETWORK_ID_LENGTH) {
            input.value = pasted // a whole ID replaces whatever was there
        } else {
            const start = input.selectionStart ?? input.value.length
            const end = input.selectionEnd ?? start
            input.setRangeText(pasted, start, end, 'end')
        }
        update(input, input.selectionStart ?? input.value.length)
    }

    async function submit(event: FormEvent) {
        event.preventDefault() // stay on this page; Go handles the result
        if (!complete || joining) {
            return
        }
        setJoining(true)
        try {
            await JoinNetwork(networkId) // on success the window closes
        } catch (err) {
            // A Go error arrives here as the rejected Promise's value.
            setError(String(err))
            setJoining(false)
        }
    }

    return (
        <form className="window join" onSubmit={submit}>
            <div className="label-row">
                <label htmlFor="network-id">Network ID</label>
                <span className="counter" aria-hidden="true">{networkId.length} / {NETWORK_ID_LENGTH}</span>
            </div>
            <input
                id="network-id"
                className="mono"
                value={networkId}
                onChange={onChange}
                onPaste={onPaste}
                placeholder={'·'.repeat(NETWORK_ID_LENGTH)}
                maxLength={NETWORK_ID_LENGTH}
                inputMode="text"
                autoComplete="off"
                autoCapitalize="off"
                autoCorrect="off"
                spellCheck={false}
                aria-describedby="network-id-help"
                aria-invalid={error !== ''}
                autoFocus
            />
            {/* One line that shows help or an error, so the window never changes height. */}
            <p id="network-id-help" className={error ? 'help error' : 'help'} role={error ? 'alert' : undefined}>
                {error || 'Find the ID in ZeroTier Central, or ask the network admin.'}
            </p>
            <div className="buttons">
                <button type="button" onClick={() => Cancel()}>Cancel</button>
                <button type="submit" className="suggested" disabled={!complete || joining}>Join</button>
            </div>
        </form>
    )
}
