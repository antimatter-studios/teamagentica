import { useEffect } from "react";

export function useVantaWaves(ref: React.RefObject<HTMLElement | null>) {
  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    let effect: any = null;
    let cancelled = false;

    // Load Three.js r134 + Vanta via CDN, same as vantajs.com does
    const threeScript = document.createElement("script");
    threeScript.src = "/three.min.js";
    threeScript.onload = () => {
      if (cancelled) return;
      const vantaScript = document.createElement("script");
      vantaScript.src = "/vanta.waves.min.js";
      vantaScript.onload = () => {
        if (cancelled || !(window as any).VANTA) return;
        effect = (window as any).VANTA.WAVES({
          el,
          mouseControls: true,
          touchControls: true,
          gyroControls: false,
          minHeight: 200.0,
          minWidth: 200.0,
          scale: 1.0,
          scaleMobile: 1.0,
          color: 0x2b46,
          shininess: 30,
          waveHeight: 15,
          waveSpeed: 1,
          zoom: 1,
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
