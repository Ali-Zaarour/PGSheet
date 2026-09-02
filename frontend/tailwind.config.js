/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      fontFamily: {
        // System stacks only. No web fonts: the app must render identically on
        // a machine with no internet access (spec §2).
        sans: ['Segoe UI', 'system-ui', '-apple-system', 'sans-serif'],
        mono: ['Cascadia Mono', 'Consolas', 'SF Mono', 'ui-monospace', 'monospace'],
      },
      colors: {
        severity: {
          error: '#b42318',
          warning: '#b54708',
          info: '#175cd3',
        },
      },
    },
  },
  plugins: [],
}
