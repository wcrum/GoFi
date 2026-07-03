/** @type {import('tailwindcss').Config} */
export default {
  presets: [require('@cnak-us/design-system/tailwind')],
  content: [
    './index.html',
    './src/**/*.{js,ts,jsx,tsx}',
    '../../cnak-design-system/src/**/*.{js,ts,jsx,tsx}',
  ],
  darkMode: 'class',
  theme: { extend: {} },
  plugins: [],
}
