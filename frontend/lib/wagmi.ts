import {getDefaultConfig} from "@rainbow-me/rainbowkit";
import {coston2} from "./chains";

export const wagmiConfig = getDefaultConfig({
  appName: "WebbPlace",
  projectId: process.env.NEXT_PUBLIC_WC_PROJECT_ID ?? "",
  chains: [coston2],
  ssr: true
});
