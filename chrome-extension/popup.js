const status = document.querySelector("#status");
const appURL = "http://127.0.0.1:8787";

document.querySelector("#grok").addEventListener("click", () => {
  chrome.tabs.create({ url: "https://grok.com/" });
});

document.querySelector("#gemini").addEventListener("click", () => {
  chrome.tabs.create({ url: "https://gemini.google.com/" });
});

document.querySelector("#app").addEventListener("click", () => {
  chrome.tabs.create({ url: appURL });
});

async function sync(provider) {
  const isGemini = provider === "gemini";
  const label = isGemini ? "Gemini" : "Grok";
  status.textContent = `Reading the current ${label} session…`;
  try {
    const cookies = await chrome.cookies.getAll({ url: isGemini ? "https://gemini.google.com/" : "https://grok.com/" });
    const signedIn = isGemini
      ? cookies.some((cookie) => cookie.name === "__Secure-1PSID" || cookie.name === "__Secure-3PSID")
      : cookies.some((cookie) => cookie.name === "sso" || cookie.name === "sso-rw");
    if (!signedIn) {
      throw new Error(`No signed-in ${label} session found. Open ${label}, sign in, and try again.`);
    }
    const headerKey = isGemini ? "geminiHeaders" : "grokHeaders";
    const stored = await chrome.storage.session.get(headerKey);
    const response = await fetch(`${appURL}/api/session/${provider}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        cookies: cookies.map(({ name, value, domain }) => ({ name, value, domain })),
        headers: stored[headerKey] || {},
      }),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || `Exporter returned HTTP ${response.status}`);
    await chrome.storage.session.remove(headerKey);
    status.textContent = `${label} connected. Open the exporter to browse your conversations.`;
  } catch (error) {
    status.textContent = error.message.includes("Failed to fetch")
      ? "The exporter is not running. Start grokslut-web, then try again."
      : error.message;
  }
}

document.querySelector("#sync-grok").addEventListener("click", () => sync("grok"));
document.querySelector("#sync-gemini").addEventListener("click", () => sync("gemini"));
