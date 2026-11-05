const providerShareRequests = new WeakMap();

window.shareProvider = async function (provider, button) {
  const draftBefore = draft;
  if (button.disabled || !draftBefore || providerShareRequests.has(draftBefore)) return;
  const request = {};
  providerShareRequests.set(draftBefore, request);
  const modal = $("#modal"), abort = new AbortController();

  const currentEditor = () => {
    if (draft !== draftBefore || editing !== provider.id || confirmAsk || modal.hidden || modal.classList.contains("out")) return null;
    return document.querySelector("#modal .editor:not(.provider-share)");
  };
  const currentShareButton = () => {
    const ed = currentEditor();
    return ed && [...ed.querySelectorAll(".provider-share-bar button")]
      .find((b) => b.title === t("Share the saved configuration, including all API keys"));
  };
  const busy = (b, on) => {
    if (!b) return;
    b.disabled = on;
    b.classList.toggle("busy", on);
    if (on) b.setAttribute("aria-busy", "true");
    else b.removeAttribute("aria-busy");
  };

  busy(button, true);
  const stop = () => {
    repaintBusy.disconnect();
    if (providerShareRequests.get(draftBefore) !== request) return;
    providerShareRequests.delete(draftBefore);
    abort.abort();
  };
  const repaintBusy = new MutationObserver(() => {
    if (providerShareRequests.get(draftBefore) !== request) {
      repaintBusy.disconnect();
      return;
    }
    if (!currentEditor()) return stop();
    busy(currentShareButton(), true);
  });
  repaintBusy.observe(modal, { childList: true, subtree: true });

  try {
    const result = await api("provider/share", { id: provider.id }, undefined, abort.signal);
    if (!currentEditor()) return;
    if (!result.link) throw new Error(t("Couldn't create a share link"));
    editorError("");
    const editor = el("div", "editor provider-share");
    const head = el("div", "ehead");
    head.append(icon(provider.icon || "generic"), el("b", "", t("Share {name}", { name: provider.name })));
    editor.append(head);
    if (result.credentials) editor.append(el("div", "warnbox", t("This link contains saved credentials, including API keys or secret headers. Anyone with the link can use them. Share it only with someone you trust.")));
    editor.append(el("p", "lib-confirm", t("Shares the saved configuration. Save changes before sharing.")));
    editor.append(el("p", "lib-confirm", t(result.keys === 1 ? "1 API key" : "{n} API keys", { n: result.keys })));
    if (!result.credentials) editor.append(el("p", "hint", t("This provider uses no saved credentials; it can be imported by another magpie on the same local network.")));
    const link = el("textarea", "provider-share-link");
    link.value = result.link;
    link.readOnly = true;
    link.rows = 5;
    link.spellcheck = false;
    link.setAttribute("aria-label", t("Import link"));
    editor.append(...field(t("Import link"), link));
    const bar = el("div", "bar");
    const cancel = el("button", "text", t("Cancel"));
    cancel.onclick = cancelAsk;
    const copyButton = el("button", "text primary", t("Copy link"));
    copyButton.onclick = () => copy(result.link, t("Import link"), copyButton);
    bar.append(el("span", "grow"), cancel, copyButton);
    editor.append(bar);
    editor.back = () => renderProviders();
    confirmAsk = editor;
    openModal(editor);
  } catch (error) {
    if (currentEditor()) {
      busy(currentShareButton(), false);
      editorError(error.message);
    }
  } finally {
    stop();
    if (button.isConnected) busy(button, false);
    busy(currentShareButton(), false);
  }
};
