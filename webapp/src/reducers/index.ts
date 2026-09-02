import {DEFAULT_TICK_SIZE} from '../constants';
import type {PluginState, StatusEntry} from '../types/store';
import {SET_STATUS, SET_STATUSES, SET_TICK_SIZE} from '../types/store';

const initialState: PluginState = {
    statuses: {},
    tickSize: DEFAULT_TICK_SIZE,
};

type SetStatusAction = {
    type: typeof SET_STATUS;
    data: StatusEntry & {postId: string};
};

type SetStatusesAction = {
    type: typeof SET_STATUSES;
    data: Record<string, StatusEntry>;
};

type SetTickSizeAction = {
    type: typeof SET_TICK_SIZE;
    data: number;
};

type PluginAction = SetStatusAction | SetStatusesAction | SetTickSizeAction;

export default function reducer(state: PluginState = initialState, action: PluginAction): PluginState {
    switch (action.type) {
    case SET_STATUS: {
        const {postId, ...entry} = action.data;
        return {
            ...state,
            statuses: {
                ...state.statuses,
                [postId]: entry,
            },
        };
    }
    case SET_STATUSES:
        return {
            ...state,
            statuses: {
                ...state.statuses,
                ...action.data,
            },
        };
    case SET_TICK_SIZE:
        return {
            ...state,
            tickSize: action.data,
        };
    default:
        return state;
    }
}
