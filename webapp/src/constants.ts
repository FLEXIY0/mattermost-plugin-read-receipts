import manifest from './manifest';

export const STATUS_UPDATED_EVENT = `custom_${manifest.id}_status_updated`;

export const READ_THRESHOLD = 0.5;

// Rendered height of the tick glyph in px; its width follows from the viewBox.
export const TICK_SIZE = 13;
