// v2v-web client: vanilla ES6, text mode only. Money is the server's: this file
// sends item ids and quantities, never an amount (docs/API.md in v2v-tma).
"use strict";

const $ = (id) => document.getElementById(id);
const state = { topics: [], topic: null, session: null, busy: false, cart: { items: [] } };

async function api(method, path, body) {
  const res = await fetch("/v1" + path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || "HTTP " + res.status);
  return data;
}

function say(who, text) {
  const el = document.createElement("div");
  el.className = "msg " + who;
  el.textContent = text; // never innerHTML: replies are model output
  $("log").append(el);
  $("log").scrollTop = $("log").scrollHeight;
  return el;
}

function qtyOf(id) {
  const l = state.cart.items.find((c) => c.item_id === id);
  return l ? l.quantity : 0;
}

// The catalog: rendered once per topic from /v1/topics, quantities refreshed from the server cart.
function renderCatalog() {
  const items = (state.topic && state.topic.items) || [];
  $("catalog").hidden = !items.length;
  $("catalog-count").textContent = items.length ? `(${items.length})` : "";
  $("items").replaceChildren(...items.map((it) => {
    const row = document.createElement("div");
    row.className = "item";
    row.id = "item-" + it.item_id;
    const icon = document.createElement("div");
    icon.className = "icon";
    icon.textContent = it.icon || "📦";
    const info = document.createElement("div");
    const title = document.createElement("div");
    title.textContent = it.title + (it.badge ? " · " + it.badge : "");
    const desc = document.createElement("div");
    desc.className = "desc";
    desc.textContent = it.description || "";
    const price = document.createElement("div");
    price.className = "price";
    price.textContent = `${it.price_uah} грн`;
    info.append(title, desc, price);
    const qty = document.createElement("div");
    qty.className = "qty";
    const minus = document.createElement("button");
    minus.type = "button"; minus.textContent = "−"; minus.setAttribute("aria-label", "Менше: " + it.title);
    const n = document.createElement("span");
    n.className = "n";
    const plus = document.createElement("button");
    plus.type = "button"; plus.textContent = "+"; plus.setAttribute("aria-label", "Більше: " + it.title);
    minus.onclick = () => bumpQty(it.item_id, -1);
    plus.onclick = () => bumpQty(it.item_id, +1);
    qty.append(minus, n, plus);
    row.append(icon, info, qty);
    return row;
  }));
  syncQty();
}

function syncQty() {
  for (const row of $("items").children) {
    const id = row.id.slice(5);
    const q = qtyOf(id);
    row.querySelector(".n").textContent = q || "";
    row.querySelector("button").disabled = q === 0; // the first button is "−"
  }
}

// Taps are queued and the target is computed when a request runs, so a fast double tap
// is +2, not two requests built from the same stale quantity.
let qtyChain = Promise.resolve();
function bumpQty(itemId, delta) {
  qtyChain = qtyChain.then(() => setQty(itemId, qtyOf(itemId) + delta));
}

async function setQty(itemId, q) {
  if (!state.session || q < 0) return;
  try {
    renderCart(await api("PUT", `/sessions/${state.session}/cart/items/${encodeURIComponent(itemId)}`, { quantity: q }));
  } catch (e) {
    say("bot", "Не вдалося змінити кошик: " + e.message);
  }
}

// HIGHLIGHT_ITEM: the server says which card the answer is about. ADD_TO_CART needs
// nothing here: the response cart (already applied) is what renderCart draws.
function applyUIActions(actions) {
  for (const a of actions || []) {
    if (a.type !== "HIGHLIGHT_ITEM" || !a.payload) continue;
    const row = document.getElementById("item-" + a.payload.item_id);
    if (!row) continue;
    row.scrollIntoView({ behavior: "smooth", block: "center" });
    row.classList.add("hl");
    setTimeout(() => row.classList.remove("hl"), a.payload.duration_ms || 2500);
  }
}

function renderCart(cart) {
  state.cart = cart || { items: [] };
  syncQty();
  const items = state.cart.items || [];
  $("cart").hidden = !state.topic || !state.topic.items.length;
  $("cart-items").replaceChildren(...items.map((it) => {
    const li = document.createElement("li");
    li.textContent = `${it.icon || ""} ${it.title} × ${it.quantity} — ${it.price_uah * it.quantity} грн`;
    return li;
  }));
  $("cart-total").textContent = items.length ? `Разом: ${cart.total_uah} грн` : "Порожньо";
  $("checkout").disabled = !items.length;
}

async function openTopic(t) {
  state.topic = t;
  state.session = null;
  $("log").replaceChildren();
  $("topics").hidden = true;
  $("chat").hidden = false;
  $("card-name").textContent = `${t.icon || ""} ${t.name}`;
  $("card-tag").textContent = t.tagline || "";
  $("prompts").replaceChildren(...(t.quick_prompts || []).map((p) => {
    const b = document.createElement("button");
    b.type = "button";
    b.textContent = p;
    b.onclick = () => send(p);
    return b;
  }));
  try {
    const s = await api("POST", "/sessions", { topic: t.id });
    state.session = s.session_id;
    if (t.welcome) say("bot", t.welcome);
    renderCatalog();
    renderCart(s.cart);
  } catch (e) {
    say("bot", "Не вдалося почати розмову: " + e.message);
  }
}

async function send(text) {
  if (!state.session || state.busy || !text.trim()) return;
  state.busy = true;
  say("user", text);
  try {
    const r = await api("POST", `/sessions/${state.session}/messages`, { text, lang: "uk" });
    say("bot", r.reply);
    renderCart(r.cart);
    applyUIActions(r.ui_actions);
  } catch (e) {
    say("bot", "Помилка: " + e.message);
  } finally {
    state.busy = false;
  }
}

const MAX_REC_SECONDS = 60;
const rec = { mr: null, chunks: [], started: 0, timer: null };

function micSupported() {
  return !!(navigator.mediaDevices && navigator.mediaDevices.getUserMedia && window.MediaRecorder);
}

async function toggleMic() {
  if (rec.mr) { rec.mr.stop(); return; }
  stopPlayback(); // tapping the mic interrupts the spoken reply, so it is not recorded back
  if (!state.session || state.busy) return;
  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: true });
  } catch {
    say("bot", "Немає доступу до мікрофона — дозвольте його в браузері або пишіть текстом.");
    return;
  }
  rec.chunks = [];
  rec.mr = new MediaRecorder(stream);
  rec.mr.ondataavailable = (e) => e.data.size && rec.chunks.push(e.data);
  rec.mr.onstop = () => {
    clearTimeout(rec.timer);
    stream.getTracks().forEach((t) => t.stop());
    const type = rec.mr.mimeType || "audio/webm";
    const secs = Math.max(1, Math.round((Date.now() - rec.started) / 1000));
    rec.mr = null;
    $("mic").classList.remove("rec");
    sendVoice(new Blob(rec.chunks, { type }), secs);
  };
  rec.started = Date.now();
  rec.mr.start();
  $("mic").classList.add("rec");
  rec.timer = setTimeout(() => rec.mr && rec.mr.stop(), MAX_REC_SECONDS * 1000);
}

async function sendVoice(blob, secs) {
  if (!state.session || state.busy || !blob.size) return;
  state.busy = true;
  const mine = say("user", "🎤 …");
  try {
    const res = await fetch(`/v1/sessions/${state.session}/voice?seconds=${secs}`, {
      method: "POST", headers: { "Content-Type": blob.type }, body: blob,
    });
    const r = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(r.error || "HTTP " + res.status);
    mine.textContent = r.transcript ? "🎤 " + r.transcript : "🎤";
    say("bot", r.reply);
    renderCart(r.cart);
    applyUIActions(r.ui_actions);
    if (r.audio_b64) play(r.audio_b64, r.audio_type);
  } catch (e) {
    mine.textContent = "🎤";
    say("bot", "Помилка: " + e.message);
  } finally {
    state.busy = false;
  }
}

let stopPlayback = () => {};

function play(b64, type) {
  stopPlayback(); // never two clips on top of each other
  const bin = atob(b64);
  const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
  const url = URL.createObjectURL(new Blob([bytes], { type }));
  const a = new Audio(url);
  const free = () => { URL.revokeObjectURL(url); a.pause(); if (stopPlayback === free) stopPlayback = () => {}; };
  stopPlayback = free;
  a.onended = a.onerror = free;
  a.play().catch(free); // autoplay can be refused; the text is already there
}

async function checkout() {
  try {
    const r = await api("POST", `/sessions/${state.session}/checkout`, {});
    say("bot", r.mock ? `Замовлення ${r.order_id} прийнято (демо, без оплати).` : "Оплата недоступна у веб-версії.");
  } catch (e) {
    say("bot", "Не вдалося оформити: " + e.message);
  }
}

function backToTopics() {
  stopPlayback();
  state.topic = null;
  state.session = null;
  $("chat").hidden = true;
  $("catalog").hidden = true;
  $("cart").hidden = true;
  $("topics").hidden = false;
}

async function init() {
  $("form").onsubmit = (ev) => { ev.preventDefault(); const v = $("text").value; $("text").value = ""; send(v); };
  $("checkout").onclick = checkout;
  $("back").onclick = backToTopics;
  if (micSupported()) { $("mic").hidden = false; $("mic").onclick = toggleMic; }
  try {
    state.topics = await api("GET", "/topics");
  } catch (e) {
    $("topics").textContent = "Сервіс тимчасово недоступний.";
    return;
  }
  $("topics").replaceChildren(...state.topics.map((t) => {
    const b = document.createElement("button");
    b.className = "topic";
    b.type = "button";
    const name = document.createElement("b");
    name.textContent = `${t.icon || ""} ${t.name}`;
    const tag = document.createElement("small");
    tag.textContent = t.tagline || "";
    b.append(name, tag);
    b.onclick = () => openTopic(t);
    return b;
  }));
}

init();
