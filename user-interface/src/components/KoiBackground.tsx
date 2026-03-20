import { useEffect, useRef } from "react";

const effects = [
  {
    script: "/vanta.waves.min.js",
    name: "WAVES",
    options: {
      color: 0x2b46,
      shininess: 30,
      waveHeight: 15,
      waveSpeed: 1,
      zoom: 1,
    },
  },
  {
    script: "/vanta.rings.min.js",
    name: "RINGS",
    options: {},
  },
  {
    script: "/vanta.net.min.js",
    name: "NET",
    options: {},
  },
];

export function useVantaWaves(ref: React.RefObject<HTMLElement | null>) {
  const chosen = useRef(effects[Math.floor(Math.random() * effects.length)]);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const { script, name, options } = chosen.current;
    let effect: any = null;
    let cancelled = false;

    const threeScript = document.createElement("script");
    threeScript.src = "/three.min.js";
    threeScript.onload = () => {
      if (cancelled) return;
      const vantaScript = document.createElement("script");
      vantaScript.src = script;
      vantaScript.onload = () => {
        if (cancelled || !(window as any).VANTA) return;
        effect = (window as any).VANTA[name]({
          el,
          mouseControls: true,
          touchControls: true,
          gyroControls: false,
          minHeight: 200.0,
          minWidth: 200.0,
          scale: 1.0,
          scaleMobile: 1.0,
          ...options,
        });
      };
      document.head.appendChild(vantaScript);
    };
    document.head.appendChild(threeScript);

    return () => {
      cancelled = true;
      effect?.destroy();
    };
  }, [ref]);
}
