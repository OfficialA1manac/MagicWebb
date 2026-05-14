import type {Config} from "tailwindcss";

export default {
  content: [
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
    "./hooks/**/*.{js,ts,jsx,tsx,mdx}",
    "./lib/**/*.{js,ts,jsx,tsx,mdx}"
  ],
  theme: {
    extend: {
      colors: {
        surface: "#0a0a0a",
        ink: "#fafafa",
        muted: "#a3a3a3",
        accent: "#10b981",
        accentHover: "#34d399",
        line: "#262626"
      }
    }
  },
  plugins: []
} satisfies Config;
