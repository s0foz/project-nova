// Project:Nova — Chat UI application logic.
// Vanilla JS module, no external dependencies, no build step.
// Talks to the local Nova HTTP API on the same origin.

const STORAGE_PREFIX = "nova.chat.";
const KEYS = {
  messages: STORAGE_PREFIX + "messages",
  model: STORAGE_PREFIX + "selectedModel",
  system: STORAGE_PREFIX + "systemPrompt",
  temperature: STORAGE_PREFIX + "temperature",
};

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const state = {
  models: [],
  selectedModel: null,
  messages: [], // {role: "user"|"assistant"|"system"|"error", content, ts, streaming?}
  streaming: false,
  abortController: null,
  systemPrompt: "",
  temperature: 0.8,
  version: "",
  rafScheduled: false,
  userScrolledUp: false,
};

// ---------------------------------------------------------------------------
// DOM references
// ---------------------------------------------------------------------------

const $ = (id) => document.getElementById(id);

const els = {
  versionBadge: $("version-badge"),
  newChat: $("new-chat"),
  settingsToggle: $("settings-toggle"),
  settingsPanel: $("settings-panel"),
  closeSettings: $("close-settings"),
  clearChat: $("clear-chat"),
  temperature: $("temperature"),
  temperatureValue: $("temperature-value"),
  sidebarToggle: $("sidebar-toggle"),
  sidebar: $("sidebar"),
  sidebarBackdrop: $("sidebar-backdrop"),
  modelSelect: $("model-select"),
  refreshModels: $("refresh-models"),
  modelList: $("model-list"),
  installedCount: $("installed-count"),
  systemPrompt: $("system-prompt"),
  systemPromptDetails: $("system-prompt-details"),
  messages: $("messages"),
  composer: $("composer"),
  messageInput: $("message-input"),
  sendButton: $("send-button"),
  stopButton: $("stop-button"),
  attachButton: $("attach-button"),
  fileInput: $("file-input"),
  statusLine: $("status-line"),
  toast: $("toast"),
};

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

function loadState() {
  try {
    state.messages = JSON.parse(localStorage.getItem(KEYS.messages) || "[]");
  } catch { state.messages = []; }
  state.selectedModel = localStorage.getItem(KEYS.model) || null;
  state.systemPrompt = localStorage.getItem(KEYS.system) || "";
  const t = parseFloat(localStorage.getItem(KEYS.temperature));
  state.temperature = Number.isFinite(t) ? t : 0.8;
}

function persist(key, value) {
  try { localStorage.setItem(key, value); } catch { /* quota or disabled */ }
}

function persistMessages() {
  // Persist a clean copy without the streaming flag.
  const clean = state.messages.map((m) => ({ role: m.role, content: m.content, ts: m.ts }));
  persist(KEYS.messages, JSON.stringify(clean));
}

// ---------------------------------------------------------------------------
// Toast / status
// ---------------------------------------------------------------------------

let toastTimer = null;
function toast(message, kind = "info", ms = 3500) {
  if (!els.toast) return;
  els.toast.textContent = message;
  els.toast.className = "toast visible" + (kind ? " " + kind : "");
  els.toast.hidden = false;
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    els.toast.classList.remove("visible");
    setTimeout(() => { if (!els.toast.classList.contains("visible")) els.toast.hidden = true; }, 250);
  }, ms);
}

function setStatus(text, kind = "") {
  if (!els.statusLine) return;
  els.statusLine.textContent = text;
  els.statusLine.className = kind;
}

// ---------------------------------------------------------------------------
// API helpers
// ---------------------------------------------------------------------------

async function apiGet(path) {
  const res = await fetch(path, { method: "GET", headers: { "Accept": "application/json" } });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`${res.status} ${res.statusText}${body ? " — " + body.slice(0, 200) : ""}`);
  }
  return res.json();
}

async function apiDelete(path, body) {
  const res = await fetch(path, {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`${res.status} ${res.statusText}${text ? " — " + text.slice(0, 200) : ""}`);
  }
  return res;
}

// ---------------------------------------------------------------------------
// Model management
// ---------------------------------------------------------------------------

async function fetchVersion() {
  try {
    const data = await apiGet("/api/version");
    state.version = (data && data.version) || "";
    els.versionBadge.textContent = state.version ? "v" + state.version : "v—";
  } catch (err) {
    els.versionBadge.textContent = "v?";
    els.versionBadge.title = "Cannot reach /api/version: " + err.message;
  }
}

async function fetchModels() {
  setStatus("Refreshing models…", "busy");
  try {
    const data = await apiGet("/api/tags");
    state.models = Array.isArray(data && data.models) ? data.models : [];
    renderModelSelect();
    renderModelList();
    setStatus(state.models.length ? `Loaded ${state.models.length} model(s).` : "No models installed.", state.models.length ? "ok" : "");
    if (state.models.length === 0) {
      renderEmptyState();
    } else {
      // If we previously selected a model that no longer exists, fall back.
      if (!state.selectedModel || !state.models.some((m) => m.name === state.selectedModel)) {
        state.selectedModel = state.models[0].name;
        persist(KEYS.model, state.selectedModel);
      }
      els.modelSelect.value = state.selectedModel;
      if (state.messages.length === 0) renderEmptyState();
    }
  } catch (err) {
    state.models = [];
    renderModelSelect();
    renderModelList();
    setStatus("Cannot reach Nova server. Is `nova serve` running?", "error");
    toast("Cannot reach Nova server: " + err.message, "error", 6000);
  }
}

function renderModelSelect() {
  const sel = els.modelSelect;
  sel.innerHTML = "";
  if (state.models.length === 0) {
    const opt = document.createElement("option");
    opt.value = "";
    opt.textContent = "No models installed";
    opt.disabled = true;
    opt.selected = true;
    sel.appendChild(opt);
    return;
  }
  for (const m of state.models) {
    const opt = document.createElement("option");
    opt.value = m.name;
    opt.textContent = m.name;
    sel.appendChild(opt);
  }
  if (state.selectedModel && state.models.some((m) => m.name === state.selectedModel)) {
    sel.value = state.selectedModel;
  } else {
    state.selectedModel = state.models[0].name;
    sel.value = state.selectedModel;
    persist(KEYS.model, state.selectedModel);
  }
}

function renderModelList() {
  const list = els.modelList;
  list.innerHTML = "";
  els.installedCount.textContent = String(state.models.length);
  if (state.models.length === 0) {
    const li = document.createElement("li");
    li.className = "muted-small";
    li.textContent = "No models installed. Pull one with `nova pull hf:owner/repo/model.gguf`.";
    list.appendChild(li);
    return;
  }
  for (const m of state.models) {
    const li = document.createElement("li");
    li.className = "model-item" + (m.name === state.selectedModel ? " active" : "");
    li.setAttribute("title", m.name + (m.details ? " — " + (m.details.parameter_size || "") : ""));

    const name = document.createElement("span");
    name.className = "model-item-name";
    name.textContent = m.name;
    li.appendChild(name);

    if (m.details && m.details.parameter_size) {
      const meta = document.createElement("span");
      meta.className = "model-item-meta";
      meta.textContent = m.details.parameter_size;
      li.appendChild(meta);
    }

    const del = document.createElement("button");
    del.className = "model-item-delete";
    del.type = "button";
    del.setAttribute("aria-label", "Delete model " + m.name);
    del.title = "Delete model";
    del.innerHTML = `<svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true"><path fill="currentColor" d="M6 19a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>`;
    del.addEventListener("click", (e) => { e.stopPropagation(); confirmDeleteModel(m.name); });
    li.appendChild(del);

    li.addEventListener("click", () => {
      if (state.selectedModel === m.name) return;
      if (state.messages.length > 0 && !confirm("Changing the model will clear the current chat. Continue?")) return;
      state.selectedModel = m.name;
      persist(KEYS.model, m.name);
      els.modelSelect.value = m.name;
      clearChat(false);
      renderModelList();
    });
    list.appendChild(li);
  }
}

async function confirmDeleteModel(name) {
  if (!confirm(`Delete model "${name}"? This removes it from disk and cannot be undone.`)) return;
  setStatus("Deleting " + name + "…", "busy");
  try {
    await apiDelete("/api/delete", { name });
    toast("Deleted " + name, "success");
    if (state.selectedModel === name) {
      state.selectedModel = null;
      persist(KEYS.model, "");
    }
    await fetchModels();
  } catch (err) {
    toast("Failed to delete: " + err.message, "error", 6000);
    setStatus("Delete failed.", "error");
  }
}

// ---------------------------------------------------------------------------
// Empty state
// ---------------------------------------------------------------------------

function renderEmptyState() {
  if (state.messages.length > 0) return;
  const wrap = document.createElement("div");
  wrap.className = "empty-state";
  wrap.innerHTML = `
    <svg class="empty-logo" viewBox="0 0 24 24" width="64" height="64" aria-hidden="true">
      <path fill="currentColor" d="M12 2 L14 10 L22 12 L14 14 L12 22 L10 14 L2 12 L10 10 Z"/>
    </svg>
    <h2>${state.models.length === 0 ? "No models installed yet" : "Start a conversation"}</h2>
    <p>${state.models.length === 0
      ? "Pull one with <code>nova pull hf:owner/repo/model.gguf</code> or create one from a Modelfile with <code>nova create</code>."
      : "Type a message below and hit Enter. Nova streams responses token by token."}</p>
  `;
  els.messages.innerHTML = "";
  els.messages.appendChild(wrap);
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

function render() {
  // Full rebuild of message list. Used for non-streaming updates.
  els.messages.innerHTML = "";
  if (state.messages.length === 0) {
    renderEmptyState();
    return;
  }
  const frag = document.createDocumentFragment();
  for (const m of state.messages) frag.appendChild(buildMessageEl(m));
  els.messages.appendChild(frag);
  attachCopyHandlers();
  maybeScrollToBottom(true);
}

function buildMessageEl(msg) {
  const wrap = document.createElement("div");
  wrap.className = "message " + (msg.role || "assistant");
  wrap.setAttribute("data-role", msg.role || "assistant");

  const avatar = document.createElement("div");
  avatar.className = "message-avatar";
  avatar.setAttribute("aria-hidden", "true");
  avatar.textContent = avatarLetter(msg.role);
  wrap.appendChild(avatar);

  const body = document.createElement("div");
  body.className = "message-body";

  const meta = document.createElement("div");
  meta.className = "message-meta";
  const role = document.createElement("span");
  role.className = "message-role";
  role.textContent = msg.role || "assistant";
  meta.appendChild(role);
  if (msg.ts) {
    const time = document.createElement("span");
    time.className = "message-time";
    time.textContent = formatTime(msg.ts);
    meta.appendChild(time);
  }
  body.appendChild(meta);

  const bubble = document.createElement("div");
  bubble.className = "message-bubble";

  if (msg.role === "error") {
    bubble.textContent = msg.content;
  } else if (msg.streaming && (!msg.content || msg.content.length === 0)) {
    const thinking = document.createElement("span");
    thinking.className = "thinking";
    thinking.setAttribute("aria-label", "Nova is thinking");
    thinking.innerHTML = "<span></span><span></span><span></span>";
    bubble.appendChild(thinking);
  } else {
    bubble.innerHTML = renderMarkdown(msg.content || "");
  }
  body.appendChild(bubble);

  wrap.appendChild(body);
  return wrap;
}

function avatarLetter(role) {
  switch (role) {
    case "user": return "U";
    case "assistant": return "N";
    case "system": return "S";
    case "error": return "!";
    default: return "?";
  }
}

function formatTime(ts) {
  try {
    const d = new Date(ts);
    if (isNaN(d.getTime())) return "";
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  } catch { return ""; }
}

// Update only the last assistant bubble during streaming for performance.
let lastStreamEl = null;
function beginStreamingAssistant() {
  render(); // rebuild so the new assistant bubble appears
  lastStreamEl = els.messages.lastElementChild;
}

function updateStreamingAssistant() {
  if (state.rafScheduled) return;
  state.rafScheduled = true;
  requestAnimationFrame(() => {
    state.rafScheduled = false;
    const msg = state.messages[state.messages.length - 1];
    if (!msg || msg.role !== "assistant") return;
    if (!lastStreamEl || !lastStreamEl.isConnected) {
      lastStreamEl = els.messages.lastElementChild;
    }
    if (!lastStreamEl) return;
    const bubble = lastStreamEl.querySelector(".message-bubble");
    if (!bubble) return;
    if (!msg.content) {
      // Still thinking.
      if (!bubble.querySelector(".thinking")) {
        bubble.innerHTML = "";
        const t = document.createElement("span");
        t.className = "thinking";
        t.innerHTML = "<span></span><span></span><span></span>";
        bubble.appendChild(t);
      }
    } else {
      bubble.innerHTML = renderMarkdown(msg.content);
      attachCopyHandlers(bubble);
    }
    maybeScrollToBottom(false);
  });
}

function finalizeStreamingAssistant() {
  if (state.rafScheduled) {
    // Ensure the final frame flushes synchronously.
    state.rafScheduled = false;
  }
  render();
  lastStreamEl = null;
}

// ---------------------------------------------------------------------------
// Copy buttons for code blocks
// ---------------------------------------------------------------------------

function attachCopyHandlers(scope) {
  const blocks = (scope || els.messages).querySelectorAll(".code-block:not([data-copy-bound])");
  for (const block of blocks) {
    block.setAttribute("data-copy-bound", "1");
    const btn = block.querySelector(".code-copy");
    if (!btn) continue;
    btn.addEventListener("click", async () => {
      const code = block.querySelector("pre code");
      const text = code ? code.textContent : "";
      try {
        await navigator.clipboard.writeText(text);
      } catch {
        // Fallback for non-secure contexts.
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand("copy"); } catch {}
        document.body.removeChild(ta);
      }
      btn.classList.add("copied");
      const original = btn.textContent;
      btn.textContent = "Copied";
      setTimeout(() => {
        btn.classList.remove("copied");
        btn.textContent = original;
      }, 1400);
    });
  }
}

// ---------------------------------------------------------------------------
// Markdown (minimal, XSS-safe: escape first, then apply transforms)
// ---------------------------------------------------------------------------

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function renderMarkdown(text) {
  if (!text) return "";

  // 1) Extract fenced code blocks first so their contents are not touched
  //    by inline transforms. We escape the code contents.
  const codeBlocks = [];
  let src = text.replace(/```([\w-]*)\n?([\s\S]*?)```/g, (m, lang, code) => {
    const langClass = lang ? "lang-" + lang.toLowerCase() : "";
    const langLabel = lang ? escapeHtml(lang.toLowerCase()) : "code";
    const idx = codeBlocks.length;
    codeBlocks.push({
      label: langLabel,
      cls: langClass,
      code: escapeHtml(code.replace(/\n$/, "")),
    });
    return `\u0000CODE${idx}\u0000`;
  });

  // 2) Escape everything else.
  src = escapeHtml(src);

  // 3) Inline code `...`
  const inlineCodes = [];
  src = src.replace(/`([^`\n]+)`/g, (m, code) => {
    const idx = inlineCodes.length;
    inlineCodes.push(`<code>${code}</code>`);
    return `\u0001INLINE${idx}\u0001`;
  });

  // 4) Headings, line by line.
  const lines = src.split(/\n/);
  const out = [];
  let inUl = false;
  let inOl = false;

  const closeLists = () => {
    if (inUl) { out.push("</ul>"); inUl = false; }
    if (inOl) { out.push("</ol>"); inOl = false; }
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();

    if (/^###\s+/.test(trimmed)) {
      closeLists();
      out.push(`<h3>${inline(trimmed.replace(/^###\s+/, ""))}</h3>`);
    } else if (/^##\s+/.test(trimmed)) {
      closeLists();
      out.push(`<h2>${inline(trimmed.replace(/^##\s+/, ""))}</h2>`);
    } else if (/^#\s+/.test(trimmed)) {
      closeLists();
      out.push(`<h1>${inline(trimmed.replace(/^#\s+/, ""))}</h1>`);
    } else if (/^\d+\.\s+/.test(trimmed)) {
      if (!inOl) { closeLists(); out.push("<ol>"); inOl = true; }
      out.push(`<li>${inline(trimmed.replace(/^\d+\.\s+/, ""))}</li>`);
    } else if (/^[-*]\s+/.test(trimmed)) {
      if (!inUl) { closeLists(); out.push("<ul>"); inUl = true; }
      out.push(`<li>${inline(trimmed.replace(/^[-*]\s+/, ""))}</li>`);
    } else if (trimmed === "") {
      closeLists();
      out.push("");
    } else {
      closeLists();
      out.push(`<p>${inline(trimmed)}</p>`);
    }
  }
  closeLists();

  let html = out.join("\n");

  // 5) Restore inline code.
  html = html.replace(/\u0001INLINE(\d+)\u0001/g, (m, i) => inlineCodes[parseInt(i, 10)] || "");

  // 6) Restore code blocks (with copy button + header).
  html = html.replace(/\u0000CODE(\d+)\u0000/g, (m, i) => {
    const b = codeBlocks[parseInt(i, 10)];
    if (!b) return "";
    return `<div class="code-block"><div class="code-block-header"><span class="code-block-lang">${b.label}</span><button class="code-copy" type="button" aria-label="Copy code">Copy</button></div><pre><code class="${b.cls}">${b.code}</code></pre></div>`;
  });

  // 7) Compact: collapse 3+ newlines into 2.
  html = html.replace(/\n{3,}/g, "\n\n");

  return html;
}

// Inline markdown transforms (applied to already-escaped text, outside pre).
function inline(s) {
  // Bold **...** then italic *...* (order matters).
  s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/(^|[^*])\*([^*]+)\*/g, "$1<em>$2</em>");
  // Links [text](url) — url must be http(s) or relative.
  s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+|\/[^\s)]*)\)/g,
    '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
  return s;
}

// ---------------------------------------------------------------------------
// Auto-scroll
// ---------------------------------------------------------------------------

function maybeScrollToBottom(force) {
  if (!force && state.userScrolledUp) return;
  const el = els.messages;
  // Use rAF so layout settles before scrolling.
  requestAnimationFrame(() => {
    el.scrollTop = el.scrollHeight;
  });
}

function trackScroll() {
  const el = els.messages;
  el.addEventListener("scroll", () => {
    const distFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    state.userScrolledUp = distFromBottom > 80;
  });
}

// ---------------------------------------------------------------------------
// Composer
// ---------------------------------------------------------------------------

function autoResize() {
  const ta = els.messageInput;
  ta.style.height = "auto";
  ta.style.height = Math.min(ta.scrollHeight, parseInt(getComputedStyle(document.documentElement).getPropertyValue("--composer-max"), 10) || 220) + "px";
}

function setStreaming(on) {
  state.streaming = on;
  els.composer.classList.toggle("streaming", on);
  els.messageInput.disabled = on;
  if (on) {
    els.stopButton.hidden = false;
    els.sendButton.hidden = true;
  } else {
    els.stopButton.hidden = true;
    els.sendButton.hidden = false;
  }
}

async function sendMessage() {
  if (state.streaming) return;
  const text = els.messageInput.value.trim();
  if (!text) return;

  if (!state.selectedModel) {
    toast("Pick or install a model first.", "warning");
    return;
  }

  // Append user message.
  state.messages.push({ role: "user", content: text, ts: Date.now() });
  persistMessages();
  els.messageInput.value = "";
  autoResize();

  // Append empty assistant message.
  state.messages.push({ role: "assistant", content: "", ts: Date.now(), streaming: true });

  render();
  beginStreamingAssistant();
  setStreaming(true);
  setStatus("Generating…", "busy");

  // Build messages payload (include system prompt if set).
  const payloadMessages = [];
  if (state.systemPrompt && state.systemPrompt.trim()) {
    payloadMessages.push({ role: "system", content: state.systemPrompt.trim() });
  }
  for (const m of state.messages) {
    if (m.role === "error") continue;
    if (m.role === "assistant" && m.streaming) continue;
    payloadMessages.push({ role: m.role, content: m.content });
  }

  const body = {
    model: state.selectedModel,
    messages: payloadMessages,
    stream: true,
    options: { temperature: state.temperature },
  };

  state.abortController = new AbortController();

  try {
    const res = await fetch("/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Accept": "application/x-ndjson" },
      body: JSON.stringify(body),
      signal: state.abortController.signal,
    });

    if (!res.ok) {
      const errBody = await res.text().catch(() => "");
      throw new Error(`${res.status} ${res.statusText}${errBody ? " — " + errBody.slice(0, 300) : ""}`);
    }
    if (!res.body) throw new Error("No response body from server.");

    await readNDJSON(res.body, (chunk) => {
      if (!chunk) return;
      let obj;
      try { obj = JSON.parse(chunk); } catch { return; }
      // Chat chunk shape: { message: { role, content }, done, ... }
      if (obj.message && typeof obj.message.content === "string") {
        const last = state.messages[state.messages.length - 1];
        if (last && last.role === "assistant") {
          last.content = (last.content || "") + obj.message.content;
          updateStreamingAssistant();
        }
      }
      if (obj.done) {
        // Finalize handled below; ignore stats fields for now.
      }
    });

    // Stream finished normally.
    const last = state.messages[state.messages.length - 1];
    if (last && last.role === "assistant") {
      last.streaming = false;
      last.ts = Date.now();
    }
    finalizeStreamingAssistant();
    persistMessages();
    setStatus("Ready.", "ok");
  } catch (err) {
    handleSendError(err);
  } finally {
    state.abortController = null;
    setStreaming(false);
  }
}

function handleSendError(err) {
  const last = state.messages[state.messages.length - 1];
  if (err && err.name === "AbortError") {
    // User pressed Stop. Keep whatever was generated.
    if (last && last.role === "assistant") {
      last.streaming = false;
      last.content = last.content || "(stopped)";
      last.ts = Date.now();
    }
    finalizeStreamingAssistant();
    persistMessages();
    setStatus("Stopped.", "");
    return;
  }
  // Surface error as a bubble (replace the empty assistant message).
  if (last && last.role === "assistant" && last.streaming) {
    state.messages.pop();
  }
  state.messages.push({
    role: "error",
    content: err && err.message ? err.message : String(err),
    ts: Date.now(),
  });
  finalizeStreamingAssistant();
  persistMessages();
  setStatus("Error: " + (err && err.message ? err.message : "unknown"), "error");
  toast("Request failed: " + (err && err.message ? err.message : "unknown"), "error", 6000);
}

// Read an NDJSON stream line by line, invoking cb for each non-empty line.
async function readNDJSON(stream, cb) {
  const reader = stream.getReader();
  const decoder = new TextDecoder("utf-8");
  let buffer = "";
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let nl;
      while ((nl = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, nl);
        buffer = buffer.slice(nl + 1);
        if (line.trim()) cb(line);
      }
    }
    // Flush trailing line without newline.
    if (buffer.trim()) cb(buffer);
  } finally {
    try { reader.releaseLock(); } catch {}
  }
}

function stopGenerating() {
  if (state.abortController) {
    try { state.abortController.abort(); } catch {}
  }
}

// ---------------------------------------------------------------------------
// Chat controls
// ---------------------------------------------------------------------------

function clearChat(showStatusMsg = true) {
  if (state.streaming) stopGenerating();
  state.messages = [];
  persistMessages();
  render();
  if (showStatusMsg) setStatus("Chat cleared.", "ok");
}

// ---------------------------------------------------------------------------
// Settings panel
// ---------------------------------------------------------------------------

function toggleSettings(force) {
  const open = force === undefined ? els.settingsPanel.hidden : force;
  els.settingsPanel.hidden = !open;
  els.settingsToggle.setAttribute("aria-expanded", open ? "true" : "false");
}

// ---------------------------------------------------------------------------
// Sidebar (mobile drawer)
// ---------------------------------------------------------------------------

function toggleSidebar(force) {
  const open = force === undefined ? !els.sidebar.classList.contains("open") : force;
  els.sidebar.classList.toggle("open", open);
  els.sidebarBackdrop.hidden = !open;
  els.sidebarToggle.setAttribute("aria-expanded", open ? "true" : "false");
}

// ---------------------------------------------------------------------------
// Wire up events
// ---------------------------------------------------------------------------

function bindEvents() {
  // Composer.
  els.composer.addEventListener("submit", (e) => {
    e.preventDefault();
    sendMessage();
  });
  els.messageInput.addEventListener("input", autoResize);
  els.messageInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      sendMessage();
    }
  });
  els.stopButton.addEventListener("click", stopGenerating);

  // Top bar.
  els.newChat.addEventListener("click", () => {
    if (state.messages.length > 0 && !confirm("Start a new chat? Current conversation will be cleared.")) return;
    clearChat();
  });
  els.settingsToggle.addEventListener("click", () => toggleSettings());
  els.closeSettings.addEventListener("click", () => toggleSettings(false));
  els.clearChat.addEventListener("click", () => {
    if (state.messages.length > 0 && !confirm("Clear all messages in this chat?")) return;
    clearChat();
  });

  // Sidebar.
  els.sidebarToggle.addEventListener("click", () => toggleSidebar());
  els.sidebarBackdrop.addEventListener("click", () => toggleSidebar(false));

  // Models.
  els.refreshModels.addEventListener("click", () => fetchModels());
  els.modelSelect.addEventListener("change", () => {
    if (state.messages.length > 0 && !confirm("Changing the model will clear the current chat. Continue?")) {
      els.modelSelect.value = state.selectedModel || "";
      return;
    }
    state.selectedModel = els.modelSelect.value;
    persist(KEYS.model, state.selectedModel);
    clearChat(false);
    renderModelList();
  });

  // System prompt.
  els.systemPrompt.addEventListener("input", () => {
    state.systemPrompt = els.systemPrompt.value;
    persist(KEYS.system, state.systemPrompt);
  });

  // Temperature.
  els.temperature.addEventListener("input", () => {
    state.temperature = parseFloat(els.temperature.value);
    els.temperatureValue.textContent = state.temperature.toFixed(2);
    persist(KEYS.temperature, String(state.temperature));
  });

  // File input (minimal wiring for future image upload).
  els.attachButton.addEventListener("click", () => {
    if (els.attachButton.disabled) return;
    els.fileInput.click();
  });
  els.fileInput.addEventListener("change", () => {
    const f = els.fileInput.files && els.fileInput.files[0];
    if (f) toast("Image attachments are not supported yet.", "warning");
    els.fileInput.value = "";
  });

  // Keyboard: Escape closes settings / sidebar.
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      if (!els.settingsPanel.hidden) toggleSettings(false);
      else if (els.sidebar.classList.contains("open")) toggleSidebar(false);
    }
  });

  // Scroll tracking.
  trackScroll();

  // Online/offline.
  window.addEventListener("online", () => {
    toast("Back online.", "success", 2000);
    fetchModels();
  });
  window.addEventListener("offline", () => {
    setStatus("Network offline.", "error");
    toast("Network offline — Nova may be unreachable.", "warning", 4000);
  });
}

// ---------------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------------

async function boot() {
  loadState();
  bindEvents();

  // Restore UI controls.
  els.systemPrompt.value = state.systemPrompt;
  els.temperature.value = state.temperature;
  els.temperatureValue.textContent = state.temperature.toFixed(2);

  // Render existing chat history (if any).
  render();

  // Fetch server version + models in parallel.
  await Promise.allSettled([fetchVersion(), fetchModels()]);
}

boot().catch((err) => {
  console.error("Nova UI boot failed:", err);
  setStatus("Failed to initialise UI: " + (err && err.message ? err.message : err), "error");
  toast("UI failed to start: " + (err && err.message ? err.message : err), "error", 8000);
});
