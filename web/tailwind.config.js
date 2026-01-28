/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  darkMode: ['class', '[data-theme="dark"]'],
  theme: {
    extend: {
      colors: {
        foreground: 'var(--text-primary)',
        background: 'var(--bg-primary)',
        primary: 'var(--primary-color)',
        secondary: 'var(--text-secondary)',
        muted: 'var(--text-tertiary)',
        border: 'var(--border-color)',
        chart: {
          1: 'var(--chart-1)',
          2: 'var(--chart-2)',
          3: 'var(--chart-3)',
          4: 'var(--chart-4)',
          5: 'var(--chart-5)',
        }
      },
    },
  },
  plugins: [],
}
