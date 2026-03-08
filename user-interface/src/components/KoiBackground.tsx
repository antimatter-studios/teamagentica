import { useEffect, useRef } from "react";
import * as THREE from "three";

/* ── Procedural koi carp swimming in a dark pond ── */

interface KoiState {
  group: THREE.Group;
  segments: THREE.Object3D[];
  // movement
  pos: THREE.Vector2;
  angle: number;
  speed: number;
  turnTarget: number;
  turnTimer: number;
  // colour
  baseColor: THREE.Color;
  accentColor: THREE.Color;
}

// ── Origami low-poly koi geometry ──

interface Station { z: number; w: number; h: number }

const BODY_STATIONS: Station[] = [
  { z: 1.8,  w: 0,    h: 0     },  // 0: nose tip
  { z: 1.3,  w: 0.10, h: 0.06  },  // 1: snout
  { z: 0.6,  w: 0.30, h: 0.16  },  // 2: forehead
  { z: 0.0,  w: 0.48, h: 0.24  },  // 3: shoulder (widest)
  { z: -0.6, w: 0.42, h: 0.20  },  // 4: mid-body
  { z: -1.2, w: 0.28, h: 0.14  },  // 5: rear
  { z: -1.8, w: 0.12, h: 0.06  },  // 6: pre-tail
  { z: -2.1, w: 0.04, h: 0.02  },  // 7: tail base
];

function buildHullSection(
  s0: Station, s1: Station, color: THREE.Color, shade: number
): THREE.Mesh {
  const c = color.clone().multiplyScalar(shade);
  const verts: number[] = [];

  // 4 vertices per station: top, right, bottom, left
  const v = (s: Station) => ({
    t: [0, s.h, s.z],
    r: [s.w, 0, s.z],
    b: [0, -s.h * 0.65, s.z],
    l: [-s.w, 0, s.z],
  });

  const a = v(s0), b = v(s1);

  // top-right quad
  verts.push(...a.t, ...a.r, ...b.r,  ...a.t, ...b.r, ...b.t);
  // bottom-right quad
  verts.push(...a.r, ...a.b, ...b.b,  ...a.r, ...b.b, ...b.r);
  // bottom-left quad
  verts.push(...a.b, ...a.l, ...b.l,  ...a.b, ...b.l, ...b.b);
  // top-left quad
  verts.push(...a.l, ...a.t, ...b.t,  ...a.l, ...b.t, ...b.l);

  const geo = new THREE.BufferGeometry();
  geo.setAttribute("position", new THREE.Float32BufferAttribute(verts, 3));
  geo.computeVertexNormals();

  const mat = new THREE.MeshStandardMaterial({
    color: c,
    flatShading: true,
    roughness: 0.55,
    metalness: 0.05,
    side: THREE.DoubleSide,
  });

  return new THREE.Mesh(geo, mat);
}

function buildTriangleMesh(
  pts: [number, number, number][],
  color: THREE.Color,
  shade: number
): THREE.Mesh {
  const verts: number[] = [];
  for (const p of pts) verts.push(...p);

  const geo = new THREE.BufferGeometry();
  geo.setAttribute("position", new THREE.Float32BufferAttribute(verts, 3));
  geo.computeVertexNormals();

  const mat = new THREE.MeshStandardMaterial({
    color: color.clone().multiplyScalar(shade),
    flatShading: true,
    roughness: 0.55,
    metalness: 0.05,
    side: THREE.DoubleSide,
  });

  return new THREE.Mesh(geo, mat);
}

function createKoi(scene: THREE.Scene): KoiState {
  const group = new THREE.Group();

  const palettes = [
    { base: "#2080e0", accent: "#90c8ff" },  // bright blue
    { base: "#1a65c0", accent: "#6cb4f0" },  // medium blue
    { base: "#e8531e", accent: "#ffccaa" },  // orange koi
    { base: "#d4382c", accent: "#f5deb3" },  // red koi
    { base: "#f28c28", accent: "#ffffff" },  // gold koi
    { base: "#2e8b8b", accent: "#a0e8e0" },  // teal
  ];
  const palette = palettes[Math.floor(Math.random() * palettes.length)];
  const baseColor = new THREE.Color(palette.base);
  const accentColor = new THREE.Color(palette.accent);

  const segments: THREE.Object3D[] = [];

  // Build body hull sections, one group per gap
  for (let i = 0; i < BODY_STATIONS.length - 1; i++) {
    const segGroup = new THREE.Group();
    const shade = 0.85 + Math.random() * 0.3;
    const useAccent = Math.random() > 0.6;
    const col = useAccent ? accentColor : baseColor;
    const hull = buildHullSection(BODY_STATIONS[i], BODY_STATIONS[i + 1], col, shade);
    segGroup.add(hull);

    // Dorsal fin — angular triangle on top, spans stations 2-5
    if (i === 2) {
      const fin = buildTriangleMesh(
        [
          [0, 0.24, 0.6],      // front base (top of shoulder)
          [0, 0.50, -0.2],     // peak
          [0, 0.20, -1.2],     // rear base
        ],
        baseColor, 0.75
      );
      segGroup.add(fin);
    }

    // Pectoral fins — small angular flaps on sides near shoulder
    if (i === 3) {
      for (const side of [-1, 1]) {
        const fin = buildTriangleMesh(
          [
            [side * 0.48, 0, 0.0],          // body attachment front
            [side * 0.70, -0.18, -0.35],     // tip (angled down and back)
            [side * 0.42, 0, -0.6],          // body attachment rear
          ],
          baseColor, 0.80
        );
        segGroup.add(fin);
      }
    }

    // Tail V-fin at the last section
    if (i === BODY_STATIONS.length - 2) {
      const tb = BODY_STATIONS[BODY_STATIONS.length - 1];
      // Upper tail prong
      const upperTail = buildTriangleMesh(
        [
          [0, tb.h, tb.z],
          [-0.28, 0.20, tb.z - 0.55],
          [0.28, 0.20, tb.z - 0.55],
        ],
        baseColor, 0.90
      );
      segGroup.add(upperTail);
      // Lower tail prong
      const lowerTail = buildTriangleMesh(
        [
          [0, -tb.h * 0.65, tb.z],
          [-0.25, -0.18, tb.z - 0.50],
          [0.25, -0.18, tb.z - 0.50],
        ],
        baseColor, 0.85
      );
      segGroup.add(lowerTail);
    }

    group.add(segGroup);
    segments.push(segGroup);
  }

  // Random start position
  const pos = new THREE.Vector2(
    (Math.random() - 0.5) * 50,
    (Math.random() - 0.5) * 30
  );
  const angle = Math.random() * Math.PI * 2;
  group.position.set(pos.x, pos.y, 0);
  group.rotation.z = angle;
  group.scale.setScalar((0.4 + Math.random() * 0.25) * 10);

  scene.add(group);

  return {
    group,
    segments,
    pos,
    angle,
    speed: 0.4 + Math.random() * 0.3,
    turnTarget: angle,
    turnTimer: Math.random() * 3,
    baseColor,
    accentColor,
  };
}

function updateKoi(koi: KoiState, dt: number, time: number, bounds: { x: number; y: number }) {
  // Decide new turn direction periodically
  koi.turnTimer -= dt;
  if (koi.turnTimer <= 0) {
    koi.turnTarget = koi.angle + (Math.random() - 0.5) * 1.8;
    koi.turnTimer = 1.5 + Math.random() * 3;
  }

  // Steer back toward center if near edges
  const distFromCenter = Math.sqrt(koi.pos.x * koi.pos.x + koi.pos.y * koi.pos.y);
  if (distFromCenter > 20) {
    const toCenter = Math.atan2(-koi.pos.y, -koi.pos.x);
    koi.turnTarget = toCenter + (Math.random() - 0.5) * 0.5;
    koi.turnTimer = 0.5;
  }

  // Smooth turn
  let angleDiff = koi.turnTarget - koi.angle;
  while (angleDiff > Math.PI) angleDiff -= Math.PI * 2;
  while (angleDiff < -Math.PI) angleDiff += Math.PI * 2;
  koi.angle += angleDiff * dt * 1.5;

  // Move forward
  koi.pos.x += Math.cos(koi.angle) * koi.speed * dt;
  koi.pos.y += Math.sin(koi.angle) * koi.speed * dt;

  // Wrap around softly
  if (koi.pos.x > bounds.x + 2) koi.pos.x = -bounds.x - 2;
  if (koi.pos.x < -bounds.x - 2) koi.pos.x = bounds.x + 2;
  if (koi.pos.y > bounds.y + 2) koi.pos.y = -bounds.y - 2;
  if (koi.pos.y < -bounds.y - 2) koi.pos.y = bounds.y + 2;

  koi.group.position.x = koi.pos.x;
  koi.group.position.y = koi.pos.y;
  koi.group.rotation.z = koi.angle - Math.PI / 2;

  // Sinusoidal body wave (swimming motion)
  const freq = 3.0;
  const amp = 0.12;
  for (let i = 0; i < koi.segments.length; i++) {
    const t = i / (koi.segments.length - 1);
    const wave = Math.sin(time * freq - t * 5) * amp * t;
    koi.segments[i].position.x = wave;
  }
}

export default function KoiBackground() {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    // ── Scene setup ──
    const scene = new THREE.Scene();
    scene.background = new THREE.Color("#060a18");
    scene.fog = new THREE.FogExp2("#060a18", 0.015);

    const camera = new THREE.PerspectiveCamera(50, window.innerWidth / window.innerHeight, 0.1, 100);
    camera.position.set(0, 0, 30);
    camera.lookAt(0, 0, 0);

    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false });
    renderer.setSize(window.innerWidth, window.innerHeight);
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    renderer.toneMapping = THREE.ACESFilmicToneMapping;
    renderer.toneMappingExposure = 1.5;
    container.appendChild(renderer.domElement);

    // ── Lighting ──
    const ambient = new THREE.AmbientLight("#4466aa", 4.0);
    scene.add(ambient);

    const topLight = new THREE.DirectionalLight("#aabbff", 3.0);
    topLight.position.set(0, 8, 5);
    scene.add(topLight);

    const accentLight = new THREE.PointLight("#66aaff", 2.5, 50);
    accentLight.position.set(-4, 3, 6);
    scene.add(accentLight);

    const warmLight = new THREE.PointLight("#ff8844", 1.5, 50);
    warmLight.position.set(4, -2, 6);
    scene.add(warmLight);

    // ── Water caustic plane ──
    const planeGeo = new THREE.PlaneGeometry(120, 80, 1, 1);
    const planeMat = new THREE.MeshStandardMaterial({
      color: "#0a1028",
      roughness: 0.9,
      metalness: 0,
    });
    const plane = new THREE.Mesh(planeGeo, planeMat);
    plane.position.z = -8;
    scene.add(plane);

    // ── Create koi ──
    const koiCount = 5;
    const kois: KoiState[] = [];
    for (let i = 0; i < koiCount; i++) {
      kois.push(createKoi(scene));
    }

    // ── Floating particles (bubbles / light specs) ──
    const particleCount = 60;
    const particleGeo = new THREE.BufferGeometry();
    const positions = new Float32Array(particleCount * 3);
    for (let i = 0; i < particleCount; i++) {
      positions[i * 3] = (Math.random() - 0.5) * 60;
      positions[i * 3 + 1] = (Math.random() - 0.5) * 40;
      positions[i * 3 + 2] = (Math.random() - 0.5) * 10;
    }
    particleGeo.setAttribute("position", new THREE.BufferAttribute(positions, 3));
    const particleMat = new THREE.PointsMaterial({
      color: "#4488cc",
      size: 0.04,
      transparent: true,
      opacity: 0.4,
    });
    const particles = new THREE.Points(particleGeo, particleMat);
    scene.add(particles);

    // ── Animation ──
    const clock = new THREE.Clock();
    let animationId: number;
    const bounds = { x: 30, y: 20 };

    function animate() {
      animationId = requestAnimationFrame(animate);
      const dt = Math.min(clock.getDelta(), 0.05);
      const time = clock.getElapsedTime();

      for (const koi of kois) {
        updateKoi(koi, dt, time, bounds);
      }

      // Gentle particle drift
      const posArr = particleGeo.attributes.position.array as Float32Array;
      for (let i = 0; i < particleCount; i++) {
        posArr[i * 3 + 1] += dt * 0.08;
        if (posArr[i * 3 + 1] > 6) posArr[i * 3 + 1] = -6;
      }
      particleGeo.attributes.position.needsUpdate = true;

      // Slow camera sway
      camera.position.x = Math.sin(time * 0.15) * 0.5;
      camera.position.y = Math.cos(time * 0.12) * 0.3;

      renderer.render(scene, camera);
    }
    animate();

    // ── Resize ──
    function onResize() {
      camera.aspect = window.innerWidth / window.innerHeight;
      camera.updateProjectionMatrix();
      renderer.setSize(window.innerWidth, window.innerHeight);
    }
    window.addEventListener("resize", onResize);

    return () => {
      window.removeEventListener("resize", onResize);
      cancelAnimationFrame(animationId);
      renderer.dispose();
      container.removeChild(renderer.domElement);
    };
  }, []);

  return (
    <div
      ref={containerRef}
      style={{
        position: "fixed",
        top: 0,
        left: 0,
        width: "100%",
        height: "100%",
        zIndex: 0,
        pointerEvents: "none",
      }}
    />
  );
}
