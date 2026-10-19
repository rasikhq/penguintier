import React from 'react'
import {createRoot} from 'react-dom/client'
import './style.css'
import Join from './Join'
import About from './About'
import {Screen} from '../wailsjs/go/main/App'

// The Go side picks the window from the command line
// (`penguintier-ui join` or `penguintier-ui about`); ask it which one to render.
Screen().then((screen) => {
    createRoot(document.getElementById('root')!).render(
        <React.StrictMode>
            {screen === 'about' ? <About/> : <Join/>}
        </React.StrictMode>
    )
})
