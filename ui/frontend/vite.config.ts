import {defineConfig, type Plugin} from 'vite'
import react from '@vitejs/plugin-react'

// Content Security Policy for the built windows: only this app's own
// scripts, styles and images, and no network access. ("data:" images are
// small files that Vite inlines, like the app icon.)
const contentSecurityPolicy = [
    "default-src 'none'",
    "script-src 'self'",
    "style-src 'self'",
    "img-src 'self' data:",
    "font-src 'self'",
    "connect-src 'self'",
    "base-uri 'none'",
    "form-action 'none'",
].join('; ')

// Adds the policy to index.html, but only in `vite build`. The dev server
// (make ui-dev) needs inline scripts and a websocket for hot reload.
function contentSecurityPolicyPlugin(): Plugin {
    return {
        name: 'penguintier-csp',
        apply: 'build',
        transformIndexHtml() {
            return [{
                tag: 'meta',
                attrs: {'http-equiv': 'Content-Security-Policy', content: contentSecurityPolicy},
                injectTo: 'head-prepend',
            }]
        },
    }
}

// https://vitejs.dev/config/
export default defineConfig({
    plugins: [react(), contentSecurityPolicyPlugin()],
    build: {
        // Vite adds a small script that teaches old browsers about
        // <link rel="modulepreload">. This page has no such links, and
        // WebKitGTK knows them anyway, so leave it out: then the bundle holds
        // only our code and the packages listed in package.json (react,
        // react-dom, scheduler), whose licenses go into the .deb and .rpm
        // (see cmd/licensegen).
        modulePreload: {polyfill: false},
    },
    server: {
        fs: {
            // The About window imports the app icon from the repo's assets/
            // folder, outside this frontend folder.
            allow: ['.', '../../assets'],
        },
    },
})
