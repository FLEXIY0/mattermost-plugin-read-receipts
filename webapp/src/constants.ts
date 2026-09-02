import manifest from './manifest';

export const STATUS_UPDATED_EVENT = `custom_${manifest.id}_status_updated`;

export const READ_THRESHOLD = 0.5;

// Rendered height of the tick glyph in px; its width follows from the viewBox.
// Overridable per instance through the plugin's TickSize setting; these bounds
// mirror server/config.go and exist so a bad value cannot break rendering.
export const DEFAULT_TICK_SIZE = 11;
export const MIN_TICK_SIZE = 8;
export const MAX_TICK_SIZE = 20;
