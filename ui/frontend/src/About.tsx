import {MouseEvent, useEffect, useState} from 'react'
import {Close, OpenRepo, Version} from '../wailsjs/go/main/App'
// The app icon, straight from the repo's icon theme folder (vite.config.ts
// allows this path), so there is only one copy of it.
import appIcon from '../../../assets/icons/hicolor/scalable/apps/io.github.rasikhq.Penguintier.svg'

// Shown as the link text. Go opens the real address (repoURL in ui/app.go).
const REPO_LABEL = 'github.com/rasikhq/penguintier'

// "v0.1.0" -> "0.1.0"; the unstamped "dev" build gets a clearer name.
function formatVersion(version: string): string {
    return version === 'dev' ? 'Development build' : `Version ${version.replace(/^v/, '')}`
}

// The About window. Closing it (Esc or the title bar) exits 0.
export default function About() {
    const [version, setVersion] = useState('')

    useEffect(() => {
        Version().then(setVersion)
    }, [])

    useEffect(() => {
        function onKeyDown(event: KeyboardEvent) {
            if (event.key === 'Escape') {
                event.preventDefault()
                Close()
            }
        }
        window.addEventListener('keydown', onKeyDown)
        return () => window.removeEventListener('keydown', onKeyDown)
    }, [])

    function openRepo(event: MouseEvent) {
        event.preventDefault() // never navigate this window; Go opens the browser
        OpenRepo()
    }

    return (
        <main className="window about">
            <img className="app-icon" src={appIcon} width={96} height={96} alt=""/>
            <h1>Penguintier</h1>
            <p className="dim">{version && formatVersion(version)}</p>
            <p className="tagline">A tray app for ZeroTier on Linux.</p>
            <a href={`https://${REPO_LABEL}`} onClick={openRepo}>{REPO_LABEL}</a>
            <footer>
                <p>MIT License · © 2026 Rasikh Q.</p>
                <p>Unofficial app. Not affiliated with ZeroTier, Inc.</p>
            </footer>
        </main>
    )
}
