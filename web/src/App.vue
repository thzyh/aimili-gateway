<script setup lang="ts">
import { RouterView } from 'vue-router'
</script>

<template>
  <RouterView />
</template>

<style>
:root {
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  color: #182230;
  background: #f7f8fa;
  font-synthesis: none;
  --panel: #ffffff;
  --input: #ffffff;
  --border: #d9e0ec;
  --text: #182230;
  --accent: #2563eb;
  --accent-soft: #eaf1ff;
  --focus: rgba(37, 99, 235, 0.18);
  --healthy: #198754;
  --degraded: #b7791f;
  --danger: #dc2626;
  --unavailable: #dc2626;
  --warning: #d97706;
  --subtle: #f5f7fa;
  --hover: #f8faff;
  --muted-bg: #f1f4f8;
  --muted-text: #667085;
  --border-soft: #edf0f4;
  --shadow: 0 12px 36px rgba(16,24,40,.06);
  --shadow-soft: 0 1px 2px rgba(16,24,40,.03);
}

* { box-sizing: border-box; }
body { margin: 0; min-width: 20rem; min-height: 100vh; }
button {
  min-height: 36px;
  padding: 0 0.9rem;
  border: 0;
  border-radius: 8px;
  background: var(--accent);
  color: #ffffff;
  cursor: pointer;
  font: inherit;
  font-weight: 700;
  font-size: 13px;
}
button.secondary { border: 1px solid var(--border); background: var(--panel); color: inherit; }
button.ghost-button { border: 1px solid var(--border); background: transparent; color: var(--muted-text); }
button:disabled { cursor: not-allowed; opacity: 0.5; }
button:focus-visible,a:focus-visible{outline:3px solid var(--focus);outline-offset:2px}

@media (prefers-color-scheme: dark) {
  :root {
    color: #e7ecf5;
    background: #111722;
    --panel: #192130;
    --input: #111722;
    --border: #303c50;
    --accent: #6f8cff;
    --accent-soft: #24345e;
    --text: #e7ecf5;
    --focus: rgba(111, 140, 255, 0.25);
    --subtle: #202a3a;
    --hover: #1e293b;
    --muted-bg: #242e40;
    --muted-text: #a9b4c7;
    --border-soft: #273247;
    --danger: #f87171;
    --warning: #fbbf24;
    --shadow: none;
    --shadow-soft: none;
  }
}
</style>
