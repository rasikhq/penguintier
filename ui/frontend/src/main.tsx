import React from 'react'
import {createRoot} from 'react-dom/client'
import './style.css'
import Join from './Join'
import About from './About'
import {AccentColor, Screen} from '../wailsjs/go/main/App'

// The Go side picks the window from the command line
// (`penguintier-ui join` or `penguintier-ui about`); ask it which one to
// render, and for the desktop's accent color ("" if it has none).
Promise.all([Screen(), AccentColor()]).then(([screen, accent]) => {
    if (accent) {
        document.documentElement.style.setProperty('--accent', accent)
    }
    createRoot(document.getElementById('root')!).render(
        <React.StrictMode>
            {screen === 'about' ? <About/> : <Join/>}
        </React.StrictMode>
    )
})
