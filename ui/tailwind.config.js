/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        'g-canvas': 'rgb(var(--g-canvas) / <alpha-value>)',
        'g-primary': 'rgb(var(--g-primary) / <alpha-value>)',
        'g-secondary': 'rgb(var(--g-secondary) / <alpha-value>)',
        'g-hover': 'rgb(var(--g-hover) / <alpha-value>)',
        'g-text': 'rgb(var(--g-text-primary) / <alpha-value>)',
        'g-text-secondary': 'rgb(var(--g-text-secondary) / <alpha-value>)',
        'g-text-disabled': 'rgb(var(--g-text-disabled) / <alpha-value>)',
        'g-accent-main': 'rgb(var(--g-accent-main) / <alpha-value>)',
        'g-border-weak': 'rgb(var(--g-border-weak) / <alpha-value>)',
        'g-border-medium': 'rgb(var(--g-border-medium) / <alpha-value>)',
        'g-border-strong': 'rgb(var(--g-border-strong) / <alpha-value>)',
      },
    },
  },
  plugins: [],
}
