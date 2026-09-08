// Production-owned concrete workflow illustration. Vendored Three.js r180 / MIT.
import * as THREE from "./vendor/three.module.min.js";

const figure = document.querySelector("[data-hero-flow]");
if (!figure) throw new Error("Hero flow figure not found");
const canvas = figure.querySelector(".scene-canvas");
const diagram = figure.querySelector(".intro-flow-diagram");
const button = figure.querySelector(".motion-control");
const reduced = matchMedia("(prefers-reduced-motion: reduce)");
const stages = [...figure.querySelectorAll(".stage")];
const status = figure.querySelector(".scene-status");
const fallbackStatus = figure.dataset.fallbackStatus || "";
const phaseStatus = (() => {
  try {
    const parsed = JSON.parse(figure.dataset.phaseStatus || "[]");
    if (
      Array.isArray(parsed) &&
      parsed.length === 4 &&
      parsed.every((phrase) => typeof phrase === "string" && phrase.length > 0)
    ) {
      return parsed;
    }
  } catch {
    // The server-rendered fallback remains usable if localized data is malformed.
  }
  return [fallbackStatus, fallbackStatus, fallbackStatus, fallbackStatus];
})();
const cycle = 16000;
let gl = null;
try {
  gl = canvas.getContext("webgl2", {
    alpha: true,
    antialias: true,
    preserveDrawingBuffer: true,
    powerPreference: "low-power",
  });
} catch {
  gl = null;
}

function showFallback() {
  figure.dataset.renderer = "fallback";
  figure.dataset.running = "false";
  button.hidden = true;
  status.textContent = fallbackStatus;
  stages.forEach((stage) => (stage.dataset.active = "false"));
}

if (gl) {
  try {
    buildScene();
  } catch {
    showFallback();
  }
} else {
  showFallback();
}

function buildScene() {
  const renderer = new THREE.WebGLRenderer({
    canvas,
    context: gl,
    alpha: true,
    antialias: true,
    preserveDrawingBuffer: true,
  });
  renderer.setClearColor(0, 0);
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = THREE.PCFSoftShadowMap;
  const scene = new THREE.Scene();
  const camera = new THREE.OrthographicCamera(-4.5, 4.5, 3.4, -3.4, 0.1, 40);
  camera.position.set(0, 0, 15);
  scene.add(new THREE.HemisphereLight(0xffffff, 0x668b85, 2));
  const key = new THREE.DirectionalLight(0xfff6e8, 2.5);
  key.position.set(-3, 7, 10);
  key.castShadow = true;
  key.shadow.mapSize.set(2048, 2048);
  Object.assign(key.shadow.camera, { left: -7, right: 7, top: 6, bottom: -6 });
  key.shadow.bias = -0.001;
  key.shadow.radius = 4;
  scene.add(key);
  const colors = {
    ivory: 0xf5f1e6,
    teal: 0x16796e,
    ink: 0x263e43,
    mint: 0xaad7c5,
    amber: 0xc68a35,
    muted: 0x9bada9,
  };
  const materials = Object.fromEntries(
    Object.entries(colors).map(([name, color]) => [
      name,
      new THREE.MeshStandardMaterial({
        color,
        roughness: 0.65,
        metalness: 0.04,
      }),
    ]),
  );
  const textures = [];
  const groups = [];
  function mesh(parent, geometry, material, x = 0, y = 0, z = 0) {
    const object = new THREE.Mesh(geometry, material);
    object.position.set(x, y, z);
    object.castShadow = true;
    object.receiveShadow = true;
    parent.add(object);
    return object;
  }
  function slab(parent, w, h, depth, material, x = 0, y = 0, z = 0) {
    const r = 0.07;
    const shape = new THREE.Shape();
    shape.moveTo(-w / 2 + r, -h / 2);
    shape.lineTo(w / 2 - r, -h / 2);
    shape.quadraticCurveTo(w / 2, -h / 2, w / 2, -h / 2 + r);
    shape.lineTo(w / 2, h / 2 - r);
    shape.quadraticCurveTo(w / 2, h / 2, w / 2 - r, h / 2);
    shape.lineTo(-w / 2 + r, h / 2);
    shape.quadraticCurveTo(-w / 2, h / 2, -w / 2, h / 2 - r);
    shape.lineTo(-w / 2, -h / 2 + r);
    shape.quadraticCurveTo(-w / 2, -h / 2, -w / 2 + r, -h / 2);
    return mesh(
      parent,
      new THREE.ExtrudeGeometry(shape, {
        depth,
        bevelEnabled: true,
        bevelThickness: 0.035,
        bevelSize: 0.035,
        bevelSegments: 3,
        steps: 1,
      }),
      material,
      x,
      y,
      z,
    );
  }
  function line(parent, points, material, radius = 0.025) {
    const curve = new THREE.CatmullRomCurve3(
      points.map((p) => new THREE.Vector3(...p)),
    );
    return mesh(
      parent,
      new THREE.TubeGeometry(curve, 32, radius, 8, false),
      material,
    );
  }
  function label(
    parent,
    text,
    width,
    height,
    x,
    y,
    z,
    color = "#263e43",
    background = null,
  ) {
    const surface = document.createElement("canvas");
    surface.width = 768;
    surface.height = 256;
    const ctx = surface.getContext("2d");
    if (background) {
      ctx.fillStyle = background;
      ctx.fillRect(0, 0, 768, 256);
    }
    ctx.fillStyle = color;
    ctx.font = "600 190px ui-monospace, monospace";
    const fontSize = Math.min(190, (190 * 700) / ctx.measureText(text).width);
    ctx.font = `600 ${fontSize}px ui-monospace, monospace`;
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillText(text, 384, 135);
    const texture = new THREE.CanvasTexture(surface);
    texture.colorSpace = THREE.SRGBColorSpace;
    textures.push(texture);
    const material = new THREE.MeshBasicMaterial({
      map: texture,
      transparent: true,
      depthWrite: false,
    });
    return mesh(
      parent,
      new THREE.PlaneGeometry(width, height),
      material,
      x,
      y,
      z + 0.065,
    );
  }
  function documentModel(parent, x, y, z, title, selected = false) {
    const doc = new THREE.Group();
    doc.position.set(x, y, z);
    parent.add(doc);
    slab(doc, 1.18, 1.62, 0.13, selected ? materials.teal : materials.ivory);
    slab(doc, 1.02, 1.44, 0.025, materials.ivory, 0, 0, 0.16);
    label(doc, title, 0.86, 0.28, 0, 0.49, 0.205);
    for (let i = 0; i < (title === "B" ? 0 : 4); i++) {
      slab(
        doc,
        0.75 - (i % 2) * 0.16,
        0.055,
        0.018,
        i === 1 ? materials.teal : materials.muted,
        -0.03,
        0.18 - i * 0.2,
        0.2,
      );
    }
    return doc;
  }
  function stageGroup(name, x, y) {
    const group = new THREE.Group();
    group.name = name;
    group.position.set(x, y, 0);
    group.rotation.set(-0.12, -0.22, -0.035);
    scene.add(group);
    groups.push(group);
    return group;
  }
  // 01: familiar evidence artifacts, each with genuine thickness.
  const evidence = stageGroup("signal-evidence", -2.2, 1.55);
  const logs = documentModel(evidence, 0.35, -0.05, 0, "LOGS");
  logs.rotation.z = -0.1;
  slab(evidence, 1.42, 0.91, 0.22, materials.ink, -0.55, 0.55, 0.35);
  line(
    evidence,
    [
      [-1.1, 0.56, 0.61],
      [-0.85, 0.56, 0.61],
      [-0.68, 0.83, 0.61],
      [-0.51, 0.26, 0.61],
      [-0.3, 0.67, 0.61],
      [-0.05, 0.56, 0.61],
    ],
    materials.mint,
    0.035,
  );
  slab(evidence, 1.02, 0.72, 0.23, materials.teal, -0.56, -0.58, 0.45);
  label(evidence, "</>", 0.85, 0.45, -0.56, -0.58, 0.7, "#fff9e9");
  // 02: linked evidence with the same highlighted snippet underneath a lens.
  const investigation = stageGroup("investigation-association", 2.05, 1.55);
  documentModel(investigation, -0.55, 0.12, -0.12, "TRACE");
  documentModel(investigation, 0.55, -0.15, 0.05, "CODE");
  line(
    investigation,
    [
      [-0.58, 0.13, 0.45],
      [-0.17, 0.48, 0.45],
      [0.55, 0.13, 0.45],
    ],
    materials.teal,
    0.033,
  );
  for (const [x, y] of [
    [-0.58, 0.13],
    [-0.17, 0.48],
    [0.55, 0.13],
  ])
    mesh(
      investigation,
      new THREE.SphereGeometry(0.075, 16, 12),
      materials.teal,
      x,
      y,
      0.45,
    );
  for (const x of [-0.55, 0.55])
    label(
      investigation,
      "timeout",
      0.78,
      0.22,
      x,
      -0.05,
      0.48,
      "#0c695d",
      "#c8e6d6",
    );
  const lens = new THREE.Group();
  lens.position.set(0.22, 0.03, 0.9);
  investigation.add(lens);
  mesh(lens, new THREE.TorusGeometry(0.53, 0.075, 16, 64), materials.ink);
  const glass = new THREE.MeshPhysicalMaterial({
    color: 0xccebe2,
    transparent: true,
    opacity: 0.17,
    roughness: 0.1,
    metalness: 0.1,
    depthWrite: false,
  });
  mesh(lens, new THREE.CircleGeometry(0.46, 48), glass, 0, 0, 0.015);
  line(
    lens,
    [
      [0.37, -0.4, 0],
      [0.82, -0.9, 0],
    ],
    materials.ink,
    0.105,
  );
  line(
    lens,
    [
      [-0.33, 0.2, 0.04],
      [-0.25, 0.33, 0.04],
      [-0.13, 0.38, 0.04],
    ],
    materials.ivory,
    0.018,
  );
  // 03: alternatives stay visible; recommendation is distinct from approval.
  const plans = stageGroup("plan-selection", -2.2, -1.45);
  const a = documentModel(plans, -0.74, 0.09, -0.18, "A");
  a.rotation.z = 0.13;
  const c = documentModel(plans, 0.72, 0.06, -0.15, "C");
  c.rotation.z = -0.12;
  const recommended = documentModel(plans, 0, -0.04, 0.46, "B");
  slab(recommended, 1.1, 0.28, 0.04, materials.teal, 0, 0.67, 0.22);
  label(recommended, "RECOMMEND", 1.0, 0.23, 0, 0.67, 0.275, "#fff9e9");
  label(recommended, "− 30s", 0.87, 0.24, 0, 0.12, 0.24, "#986149");
  label(recommended, "+ 60s", 0.87, 0.24, 0, -0.12, 0.24, "#16796e");
  // 04: a sculpted head, neck and shoulders beside a pending checklist.
  const review = stageGroup("human-review", 2.05, -1.45);
  mesh(
    review,
    new THREE.SphereGeometry(0.34, 40, 32),
    materials.ivory,
    -0.67,
    0.43,
    0.4,
  ).scale.set(0.88, 1.1, 0.87);
  mesh(
    review,
    new THREE.SphereGeometry(0.35, 32, 24),
    materials.ink,
    -0.67,
    0.56,
    0.25,
  ).scale.set(0.96, 0.88, 0.85);
  mesh(
    review,
    new THREE.CylinderGeometry(0.13, 0.16, 0.27, 24),
    materials.ivory,
    -0.67,
    0.02,
    0.36,
  );
  const shoulders = mesh(
    review,
    new THREE.SphereGeometry(0.58, 40, 24),
    materials.teal,
    -0.67,
    -0.47,
    0.29,
  );
  shoulders.scale.set(1, 0.75, 0.57);
  slab(review, 0.95, 0.2, 0.31, materials.ink, -0.67, -0.82, 0.13);
  for (const x of [-0.77, -0.58])
    mesh(
      review,
      new THREE.SphereGeometry(0.025, 12, 8),
      materials.ink,
      x,
      0.47,
      0.69,
    );
  const checklist = documentModel(review, 0.59, -0.03, 0.05, "REVIEW");
  slab(checklist, 0.38, 0.15, 0.08, materials.ink, 0, 0.84, 0.11);
  for (let i = 0; i < 3; i++) {
    slab(
      checklist,
      0.12,
      0.12,
      0.02,
      materials.teal,
      -0.36,
      0.21 - i * 0.22,
      0.24,
    );
    label(checklist, "✓", 0.12, 0.15, -0.36, 0.21 - i * 0.22, 0.28, "#fff9e9");
  }
  slab(checklist, 0.95, 0.33, 0.07, materials.amber, 0, -0.57, 0.24);
  label(checklist, "PENDING", 0.89, 0.25, 0, -0.57, 0.325, "#302b22");
  // A matte backing receives soft shadows without becoming a machine platform.
  const shadow = new THREE.ShadowMaterial({ opacity: 0.025 });
  mesh(scene, new THREE.PlaneGeometry(12, 10), shadow, 0, 0, -0.65).castShadow =
    false;
  const routes = [
    [
      [-0.85, 1.65, 0.15],
      [-0.35, 1.65, 0.15],
      [0.15, 1.65, 0.15],
      [0.7, 1.65, 0.15],
    ],
    [
      [2.0, 0.55, -0.1],
      [1.1, 0.05, -0.1],
      [-1.05, 0.05, -0.1],
      [-2.2, -0.36, -0.1],
    ],
    [
      [-0.85, -1.5, 0.15],
      [-0.35, -1.5, 0.15],
      [0.15, -1.5, 0.15],
      [0.62, -1.5, 0.15],
    ],
  ].map(
    (points) =>
      new THREE.CatmullRomCurve3(points.map((p) => new THREE.Vector3(...p))),
  );
  const packets = [];
  routes.forEach((route, index) => {
    mesh(
      scene,
      new THREE.TubeGeometry(route, 48, 0.018, 8, false),
      materials.muted,
    );
    const end = route.getPoint(0.98),
      tangent = route.getTangent(0.98);
    const arrow = mesh(
      scene,
      new THREE.ConeGeometry(0.075, 0.18, 16),
      index === 2 ? materials.amber : materials.teal,
      end.x,
      end.y,
      end.z,
    );
    arrow.quaternion.setFromUnitVectors(new THREE.Vector3(0, 1, 0), tangent);
    for (let j = 0; j < 8; j++)
      packets.push({
        object: mesh(
          scene,
          new THREE.SphereGeometry(0.033, 10, 8),
          index === 2 ? materials.amber : materials.teal,
        ),
        route,
        index,
        offset: j / 8,
      });
  });
  let elapsed = 0,
    lastTime = null,
    frame = 0,
    inView = false,
    paused = false,
    lost = false,
    renderCount = 0,
    lastPhase = -1;
  function renderScene() {
    if (lost) return;
    const t = reduced.matches ? 13200 : elapsed % cycle;
    const phase = t < 3600 ? 0 : t < 7600 ? 1 : t < 10800 ? 2 : 3;
    if (phase !== lastPhase) {
      figure.dataset.phase = String(phase);
      status.textContent = phaseStatus[phase];
      stages.forEach(
        (stage, i) => (stage.dataset.active = String(i === phase)),
      );
      lastPhase = phase;
    }
    packets.forEach(({ object, route, index, offset }) => {
      const progress =
        phase === index ? ((t % 4000) / 4000 + offset) % 1 : offset;
      object.position.copy(route.getPoint(progress * 0.94));
      object.scale.setScalar(phase === index ? 1.3 : 0.65);
    });
    recommended.position.z =
      0.46 + (phase === 2 ? Math.sin(((t - 7600) / 3200) * Math.PI) * 0.07 : 0);
    renderer.render(scene, camera);
    renderCount++;
    Object.assign(figure.dataset, {
      elapsed: String(Math.round(elapsed)),
      frames: String(renderCount),
      meshes: String(renderer.info.render.calls),
      connectors: String(routes.length),
      gate: "human-review",
      output: "none",
      reviewState: "pending",
      models: groups.map((g) => g.name).join(","),
      drawCalls: String(renderer.info.render.calls),
    });
  }
  function animate(now) {
    frame = 0;
    if (lastTime === null) lastTime = now;
    if (now - lastTime >= 1000 / 30) {
      elapsed += Math.min(now - lastTime, 100);
      lastTime = now;
      renderScene();
    }
    frame = requestAnimationFrame(animate);
  }
  function syncPlayback() {
    cancelAnimationFrame(frame);
    frame = 0;
    lastTime = null;
    const running =
      !paused && !reduced.matches && inView && !document.hidden && !lost;
    button.disabled = reduced.matches;
    button.dataset.paused = String(paused || reduced.matches);
    const buttonLabel = reduced.matches
      ? figure.dataset.staticLabel
      : paused
        ? figure.dataset.playLabel
        : figure.dataset.pauseLabel;
    button.querySelector(".motion-label").textContent = buttonLabel;
    button.setAttribute("aria-label", buttonLabel);
    figure.dataset.running = String(running);
    renderScene();
    if (running) frame = requestAnimationFrame(animate);
  }
  function resizeScene() {
    const { width, height } = diagram.getBoundingClientRect();
    if (!width || !height || lost) return;
    renderer.setPixelRatio(Math.min(devicePixelRatio || 1, 2));
    renderer.setSize(width, height, false);
    camera.left = -4.35;
    camera.right = 4.35;
    camera.top = (4.35 * height) / width;
    camera.bottom = -camera.top;
    camera.updateProjectionMatrix();
    renderScene();
  }
  function handleContextLoss(event) {
    event.preventDefault();
    lost = true;
    cancelAnimationFrame(frame);
    figure.dataset.renderer = "fallback";
    figure.dataset.running = "false";
    button.hidden = true;
    status.textContent = fallbackStatus;
    stages.forEach((stage) => (stage.dataset.active = "false"));
  }
  canvas.addEventListener("webglcontextlost", handleContextLoss);
  button.addEventListener("click", () => {
    paused = !paused;
    syncPlayback();
  });
  reduced.addEventListener("change", syncPlayback);
  document.addEventListener("visibilitychange", syncPlayback);
  const resizeObserver = new ResizeObserver(resizeScene);
  resizeObserver.observe(diagram);
  window.addEventListener("resize", resizeScene);
  const intersection = new IntersectionObserver(([entry]) => {
    inView = entry.isIntersecting;
    syncPlayback();
  });
  intersection.observe(diagram);
  resizeScene();
  figure.dataset.renderer = "webgl";
  button.hidden = false;
  syncPlayback();
  window.addEventListener("pagehide", (event) => {
    cancelAnimationFrame(frame);
    figure.dataset.running = "false";
    if (event.persisted) return;
    lost = true;
    resizeObserver.disconnect();
    intersection.disconnect();
    const disposed = new Set();
    scene.traverse((object) => {
      object.geometry?.dispose();
      if (object.material && !disposed.has(object.material)) {
        object.material.dispose();
        disposed.add(object.material);
      }
    });
    textures.forEach((texture) => texture.dispose());
    renderer.dispose();
  });
  window.addEventListener("pageshow", syncPlayback);
}
