export type MessageStatusValue = 'delivered' | 'read';

export type StatusEntry = {
    status: MessageStatusValue;
    readBy: string[];
};

export type PluginState = {
    statuses: Record<string, StatusEntry>;
};

export const PLUGIN_STATE_KEY = 'plugins-com.github.mattermost-message-status';

// Declared as literal types (not plain `string`) so the reducer's action union
// stays discriminated.
export const SET_STATUS = `PLUGIN_${PLUGIN_STATE_KEY}_SET_STATUS` as const;
export const SET_STATUSES = `PLUGIN_${PLUGIN_STATE_KEY}_SET_STATUSES` as const;

export type StatusUpdatePayload = {
    post_id: string;
    channel_id: string;
    author_id: string;
    status: MessageStatusValue;
    read_by: string[];
};

export type StatusResponse = {
    post_id: string;
    status: MessageStatusValue;
    read_by: string[];
};
