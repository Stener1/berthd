// micromark escapes attributes, but an <img> would still make the webview
// fetch its src before React could decide whether it belongs to this
// worktree. Replace every Markdown image with an inert slot first. The chat
// fills only verified worktree pictures into those slots.
export function imageSlots(html: string): string {
  return html.replace(/<img\b[^>]*>/g, (tag) => {
    const src = /\bsrc="([^"]*)"/.exec(tag)?.[1];
    const alt = /\balt="([^"]*)"/.exec(tag)?.[1] ?? "";
    if (!src) return `<span class="cv-image-source">${alt}</span>`;
    return `<span class="cv-image-slot" data-chat-image="${src}" data-chat-image-alt="${alt}"></span>`;
  });
}
