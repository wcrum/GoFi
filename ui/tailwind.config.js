/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        'g-canvas': 'rgb(var(--g-canvas) / <alpha-value>)',
        'g-primary': 'rgb(var(--g-primary) / <alpha-value>)',
        'g-secondary': 'rgb(var(--g-secondary) / <alpha-value>)',
        'g-hover': '#1e3045',
        'g-text': '#e7eef8',
        'g-text-secondary': '#96a8be',
        'g-text-disabled': '#64778f',
        'g-accent-main': 'rgb(var(--g-accent-main) / <alpha-value>)',
        'g-border-weak': '#24354a',
        'g-border-medium': '#3a506a',
        'g-border-strong': '#7087a2',
      },
    },
  },
  plugins: [],
}
