export function isElementVisible(element: HTMLElement, threshold: number): boolean {
    const rect = element.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) {
        return false;
    }

    const viewportTop = 0;
    const viewportBottom = window.innerHeight || document.documentElement.clientHeight;
    const visibleTop = Math.max(rect.top, viewportTop);
    const visibleBottom = Math.min(rect.bottom, viewportBottom);
    const visibleHeight = Math.max(0, visibleBottom - visibleTop);

    // Measure against the viewport rather than the post when the post is taller
    // than the screen. Dividing by rect.height makes the threshold unreachable
    // for long messages and tall images — a 2000px post in an 800px viewport
    // tops out at 0.4 — so those were never marked read however long you
    // stared at them.
    const reference = Math.min(rect.height, viewportBottom);
    if (reference <= 0) {
        return false;
    }

    return (visibleHeight / reference) >= threshold;
}
