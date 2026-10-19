import {useEffect, useState} from 'react'
import {Close, Version} from '../wailsjs/go/main/App'

// Placeholder About window from P0. Phase P2 builds the real one.
export default function About() {
    const [version, setVersion] = useState('')

    useEffect(() => {
        Version().then(setVersion)
    }, [])

    return (
        <main className="window">
            <h1>Penguintier</h1>
            <p>Version {version}</p>
            <p>A system tray app for ZeroTier on Linux.</p>
            <p className="note">Unofficial; not affiliated with ZeroTier, Inc.</p>
            <p className="note">MIT License · github.com/rasikhq/penguintier</p>
            <div className="buttons">
                <button type="button" onClick={() => Close()}>Close</button>
            </div>
        </main>
    )
}
