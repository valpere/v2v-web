// v2v-web client: vanilla ES6, text mode only. Money is the server's: this file
// sends item ids and quantities, never an amount (docs/API.md in v2v-tma).
"use strict";

const $ = (id) => document.getElementById(id);
const state = { topics: [], topic: null, session: null, busy: false };

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
}

function renderCart(cart) {
  const items = (cart && cart.items) || [];
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
  $("topic-name").textContent = `${t.icon || ""} ${t.name}`;
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
    say("bot", t.welcome || t.tagline || "");
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
  } catch (e) {
    say("bot", "Помилка: " + e.message);
  } finally {
    state.busy = false;
  }
}

async function checkout() {
  try {
    const r = await api("POST", `/sessions/${state.session}/checkout`, {});
    say("bot", r.mock ? `Замовлення ${r.order_id} прийнято (демо, без оплати).` : "Оплата недоступна у веб-версії.");
  } catch (e) {
    say("bot", "Не вдалося оформити: " + e.message);
  }
}

async function init() {
  $("form").onsubmit = (ev) => { ev.preventDefault(); const v = $("text").value; $("text").value = ""; send(v); };
  $("back").onclick = () => { $("chat").hidden = true; $("cart").hidden = true; $("topics").hidden = false; $("topic-name").textContent = ""; };
  $("checkout").onclick = checkout;
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
