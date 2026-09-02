import type {Store} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import manifest from '../manifest';
import type {MessageStatusValue, StatusEntry, StatusResponse, StatusUpdatePayload} from '../types/store';
import {PLUGIN_STATE_KEY, SET_STATUS, SET_STATUSES} from '../types/store';

export function getPluginState(state: GlobalState) {
    return (state as GlobalState & Record<string, {statuses: Record<string, StatusEntry>}>)[PLUGIN_STATE_KEY];
}

export function getPostStatus(state: GlobalState, postId: string): StatusEntry | undefined {
    return getPluginState(state)?.statuses?.[postId];
}

export function setPostStatus(store: Store<GlobalState>, postId: string, entry: StatusEntry): void {
    store.dispatch({
        type: SET_STATUS,
        data: {
            postId,
            ...entry,
        },
    });
}

export function setPostStatuses(store: Store<GlobalState>, statuses: Record<string, StatusEntry>): void {
    store.dispatch({
        type: SET_STATUSES,
        data: statuses,
    });
}

function parseReadBy(value: unknown): string[] {
    if (Array.isArray(value)) {
        return value.filter((item): item is string => typeof item === 'string' && item.length > 0);
    }

    if (typeof value === 'string' && value.length > 0) {
        return value.split(',').map((item) => item.trim()).filter(Boolean);
    }

    return [];
}

function normalizeStatusPayload(payload: unknown): StatusUpdatePayload | null {
    if (!payload || typeof payload !== 'object') {
        return null;
    }

    const data = payload as Record<string, unknown>;
    const postId = data.post_id;
    const status = data.status;

    if (typeof postId !== 'string' || (status !== 'delivered' && status !== 'read')) {
        return null;
    }

    return {
        post_id: postId,
        channel_id: typeof data.channel_id === 'string' ? data.channel_id : '',
        author_id: typeof data.author_id === 'string' ? data.author_id : '',
        status: status as MessageStatusValue,
        read_by: parseReadBy(data.read_by),
    };
}

export function applyStatusUpdate(store: Store<GlobalState>, payload: unknown): void {
    const normalized = normalizeStatusPayload(payload);
    if (!normalized) {
        return;
    }

    const state = store.getState();
    const currentUserId = state.entities.users.currentUserId;
    const post = state.entities.posts.posts[normalized.post_id];

    if (currentUserId && post && post.user_id !== currentUserId) {
        return;
    }

    if (currentUserId && normalized.author_id && !post && normalized.author_id !== currentUserId) {
        return;
    }

    const existing = getPostStatus(state, normalized.post_id);
    const incoming: StatusEntry = {
        status: normalized.status,
        readBy: normalized.read_by,
    };

    setPostStatus(store, normalized.post_id, mergeStatusEntry(existing, incoming));
}

function statusRank(status: MessageStatusValue): number {
    return status === 'read' ? 2 : 1;
}

function mergeStatusEntry(existing: StatusEntry | undefined, incoming: StatusEntry): StatusEntry {
    if (!existing) {
        return incoming;
    }

    if (statusRank(incoming.status) >= statusRank(existing.status)) {
        return incoming;
    }

    return existing;
}

export function applyStatusResponses(store: Store<GlobalState>, responses: StatusResponse[]): void {
    const state = store.getState();
    const statuses: Record<string, StatusEntry> = {};

    responses.forEach((response) => {
        if (!response.post_id || !response.status) {
            return;
        }

        const incoming: StatusEntry = {
            status: response.status,
            readBy: response.read_by || [],
        };

        statuses[response.post_id] = mergeStatusEntry(getPostStatus(state, response.post_id), incoming);
    });

    if (Object.keys(statuses).length > 0) {
        setPostStatuses(store, statuses);
    }
}

export function setOptimisticDelivered(store: Store<GlobalState>, postIds: string[]): void {
    postIds.forEach((postId) => {
        const existing = getPostStatus(store.getState(), postId);
        if (existing?.status === 'read') {
            return;
        }

        if (existing?.status === 'delivered') {
            return;
        }

        setPostStatus(store, postId, {
            status: 'delivered',
            readBy: [],
        });
    });
}

function getCsrfToken(): string {
    if (typeof document === 'undefined' || !document.cookie) {
        return '';
    }

    const cookies = document.cookie.split(';');
    for (const cookie of cookies) {
        const trimmed = cookie.trim();
        if (trimmed.startsWith('MMCSRF=')) {
            return trimmed.slice('MMCSRF='.length);
        }
    }

    return '';
}

// Mattermost can be served from a subpath (SiteURL like https://host/chat), in
// which case the webapp exposes it as window.basename. Hard-coding a root-
// relative URL would 404 on those installs.
function getSiteBasePath(): string {
    const {basename} = window as Window & {basename?: string};
    if (typeof basename !== 'string' || basename === '/') {
        return '';
    }

    return basename.replace(/\/+$/, '');
}

function getPluginApiBase(): string {
    return `${getSiteBasePath()}/plugins/${encodeURIComponent(manifest.id)}/api/v1`;
}

function getRequestHeaders(method = 'GET'): Record<string, string> {
    const headers: Record<string, string> = {
        'X-Requested-With': 'XMLHttpRequest',
    };

    if (method.toLowerCase() !== 'get') {
        const csrf = getCsrfToken();
        if (csrf) {
            headers['X-CSRF-Token'] = csrf;
        }
    }

    return headers;
}

export async function markPostAsRead(postId: string): Promise<StatusResponse | null> {
    const response = await fetch(`${getPluginApiBase()}/read`, {
        method: 'POST',
        credentials: 'include',
        headers: {
            ...getRequestHeaders('POST'),
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({post_id: postId}),
    });

    if (!response.ok) {
        return null;
    }

    try {
        const data = await response.json() as Omit<Partial<StatusResponse>, 'status'> & {status?: string};
        if (data?.post_id && (data.status === 'delivered' || data.status === 'read')) {
            return {
                post_id: data.post_id,
                status: data.status,
                read_by: data.read_by || [],
            };
        }

        if (data?.status === 'ignored') {
            return {
                post_id: postId,
                status: 'delivered',
                read_by: [],
            };
        }
    } catch {
        return null;
    }

    return null;
}

// The server caps how many ids one status request may carry, so batch rather
// than let a long scrollback silently lose the tail of the list.
const STATUS_BATCH_SIZE = 100;

async function fetchStatusBatch(postIds: string[]): Promise<StatusResponse[]> {
    const response = await fetch(`${getPluginApiBase()}/status?post_ids=${encodeURIComponent(postIds.join(','))}`, {
        credentials: 'include',
        headers: getRequestHeaders('GET'),
    });

    if (!response.ok) {
        return [];
    }

    try {
        const data = await response.json() as {statuses?: StatusResponse[]};
        return Array.isArray(data?.statuses) ? data.statuses : [];
    } catch {
        return [];
    }
}

export async function fetchPostStatuses(postIds: string[]): Promise<StatusResponse[]> {
    const uniqueIds = Array.from(new Set(postIds.filter(Boolean)));
    if (uniqueIds.length === 0) {
        return [];
    }

    const batches: string[][] = [];
    for (let i = 0; i < uniqueIds.length; i += STATUS_BATCH_SIZE) {
        batches.push(uniqueIds.slice(i, i + STATUS_BATCH_SIZE));
    }

    const results = await Promise.all(batches.map(fetchStatusBatch));
    return results.flat();
}

export async function hydratePostStatuses(store: Store<GlobalState>, postIds: string[]): Promise<void> {
    const uniqueIds = Array.from(new Set(postIds.filter(Boolean)));
    if (uniqueIds.length === 0) {
        return;
    }

    const responses = await fetchPostStatuses(uniqueIds);
    if (responses.length === 0) {
        return;
    }

    applyStatusResponses(store, responses);
}
