<script setup lang="ts">
import { RouterView } from 'vue-router'
</script>

<template>
  <RouterView />
</template>

<style>
:root {
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  color: #172033;
  background: #f3f6fb;
  font-synthesis: none;
  --panel: #ffffff;
  --input: #ffffff;
  --border: #d9e0ec;
  --accent: #3457d5;
  --focus: rgba(52, 87, 213, 0.2);
  --healthy: #198754;
  --degraded: #b7791f;
  --unavailable: #c23b3b;
  --muted-bg: #eef2f8;
  --muted-text: #5e6b80;
  --shadow: 0 1rem 3rem rgba(42, 55, 86, 0.08);
}

* { box-sizing: border-box; }
body { margin: 0; min-width: 20rem; min-height: 100vh; }
button {
  padding: 0.75rem 1rem;
  border: 0;
  border-radius: 0.65rem;
  background: var(--accent);
  color: #ffffff;
  cursor: pointer;
  font: inherit;
  font-weight: 750;
}
button.secondary { border: 1px solid var(--border); background: var(--panel); color: inherit; }
button:disabled { cursor: wait; opacity: 0.65; }

@media (prefers-color-scheme: dark) {
  :root {
    color: #e7ecf5;
    background: #111722;
    --panel: #192130;
    --input: #111722;
    --border: #303c50;
    --accent: #6f8cff;
    --focus: rgba(111, 140, 255, 0.25);
    --muted-bg: #242e40;
    --muted-text: #a9b4c7;
    --shadow: none;
  }
}
</style>
