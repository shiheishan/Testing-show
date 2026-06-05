import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  // The frontend source lives in web/, but the build output stays at the repo
  // root dist/ so the Go server's os.DirFS("dist") and the release packaging
  // need no path changes. emptyOutDir is required because dist/ is outside the
  // Vite project root.
  build: {
    outDir: "../dist",
    emptyOutDir: true,
  },
})
