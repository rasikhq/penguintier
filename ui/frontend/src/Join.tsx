import {FormEvent, useState} from 'react'
import {Cancel, Join as JoinNetwork} from '../wailsjs/go/main/App'

// Placeholder Join dialog from P0. Phase P2 builds the real one
// (hex-only input, Join disabled until 16 characters, Enter/Esc).
// The Go side validates the ID, prints it and exits 0; Cancel exits 1.
export default function Join() {
    const [networkId, setNetworkId] = useState('')
    const [error, setError] = useState('')

    async function submit(event: FormEvent) {
        event.preventDefault()
        try {
            await JoinNetwork(networkId)
        } catch (err) {
            // A Go error arrives here as the rejected Promise's value.
            setError(String(err))
        }
    }

    return (
        <form className="window" onSubmit={submit}>
            <h1>Join Network</h1>
            <p className="note">Placeholder window. The real dialog arrives in P2.</p>
            <input
                value={networkId}
                onChange={(event) => setNetworkId(event.target.value)}
                placeholder="16-digit network ID"
                maxLength={16}
                spellCheck={false}
                autoFocus
            />
            {/* Always rendered, so the window height doesn't change when an error appears. */}
            <p className="error" role="alert">{error}</p>
            <div className="buttons">
                <button type="button" onClick={() => Cancel()}>Cancel</button>
                <button type="submit">Join</button>
            </div>
        </form>
    )
}
