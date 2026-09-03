import type {Post} from '@mattermost/types/posts';
import type {Channel} from '@mattermost/types/channels';
import type {GlobalState} from '@mattermost/types/store';

type ExtendedGlobalState = GlobalState & {
    views?: {
        lhs?: {
            currentStaticPageId?: string;
        };
        rhs?: {
            selectedPostId?: string;
            isSidebarOpen?: boolean;
        };
        rhsSuppressed?: boolean;
        threads?: {
            selectedThreadIdInTeam?: Record<string, string | null>;
        };
    };
};

const GLOBAL_THREADS_PAGE_ID = 'threads';

export function isGlobalThreadsViewActive(state: ExtendedGlobalState): boolean {
    return state.views?.lhs?.currentStaticPageId === GLOBAL_THREADS_PAGE_ID;
}

export function getRhsThreadRootId(state: ExtendedGlobalState): string | undefined {
    if (!state.views?.rhs?.isSidebarOpen || state.views?.rhsSuppressed) {
        return undefined;
    }

    return state.views.rhs.selectedPostId || undefined;
}

export function getGlobalThreadsRootId(state: ExtendedGlobalState): string | undefined {
    const currentTeamId = state.entities.teams.currentTeamId;
    if (!currentTeamId) {
        return undefined;
    }

    const rootId = state.views?.threads?.selectedThreadIdInTeam?.[currentTeamId];
    return rootId || undefined;
}

export function getSelectedThreadRootId(state: ExtendedGlobalState): string | undefined {
    if (isGlobalThreadsViewActive(state)) {
        return getGlobalThreadsRootId(state);
    }

    return getRhsThreadRootId(state);
}

function addThreadPosts(state: ExtendedGlobalState, postIds: Set<string>, rootId: string): void {
    postIds.add(rootId);
    getPostIdsInThread(state, rootId).forEach((replyId) => {
        postIds.add(replyId);
    });
}

export function isPendingPost(post: Post): boolean {
    return Boolean(post.pending_post_id && post.id === post.pending_post_id);
}

// Mattermost plugins (e.g. Channel Reply) use custom_* post types for user-authored content.
export function isUserContentPostType(type?: string): boolean {
    if (!type) {
        return true;
    }

    if (type.startsWith('system_')) {
        return false;
    }

    if (type.startsWith('custom_')) {
        return true;
    }

    return false;
}

export function isCustomPluginPost(post: {type?: string}): boolean {
    return Boolean(post.type?.startsWith('custom_')) && isUserContentPostType(post.type);
}

export function isEligiblePost(post: Post | undefined): post is Post {
    if (!post) {
        return false;
    }

    if (post.delete_at > 0) {
        return false;
    }

    if (!isUserContentPostType(post.type)) {
        return false;
    }

    if (post.props?.from_webhook) {
        return false;
    }

    if (post.props?.from_bot === 'true' || post.props?.from_bot === true) {
        return false;
    }

    if (isPendingPost(post)) {
        return false;
    }

    return true;
}

// The ticks are absolutely positioned over the bottom-right of the post body,
// so on a post that ends in an image or a link preview they sit on top of that
// picture rather than on the theme background — and a theme-coloured tick is
// invisible over a photo of the wrong tone. Such posts get their own scrim
// instead; see `.message-status-ticks-attachment[data-over-media='true']`.
export function postHasMedia(post: Post | undefined): boolean {
    if (!post) {
        return false;
    }

    if (post.file_ids?.length) {
        return true;
    }

    const metadata = post.metadata as Post['metadata'] | undefined;
    if (metadata?.files?.length) {
        return true;
    }

    // Populated for markdown images and for link-preview thumbnails alike.
    if (metadata?.images && Object.keys(metadata.images).length > 0) {
        return true;
    }

    return Boolean(metadata?.embeds?.some((embed) => embed?.type === 'image'));
}

export function isOwnPost(post: Post, currentUserId: string): boolean {
    return post.user_id === currentUserId;
}

export function isDirectChannel(channel: Channel | undefined): boolean {
    return channel?.type === 'D';
}

export function getOtherUserIdInDM(channel: Channel | undefined, currentUserId: string): string | undefined {
    if (!channel || channel.type !== 'D') {
        return undefined;
    }

    const userIds = channel.name.split('__');
    return userIds.find((id) => id !== currentUserId);
}

export function getUserDisplayName(state: {entities: {users: {profiles: Record<string, {username?: string; first_name?: string; last_name?: string; nickname?: string}>}}}, userId: string): string {
    const user = state.entities.users.profiles[userId];
    if (!user) {
        return userId;
    }

    if (user.nickname) {
        return user.nickname;
    }

    const fullName = `${user.first_name || ''} ${user.last_name || ''}`.trim();
    if (fullName) {
        return fullName;
    }

    return user.username || userId;
}

// Every place Mattermost may have rendered this post. The same post is on
// screen twice whenever a thread is open — `post_<id>` in the centre channel and
// `rhsPost_<id>` in the sidebar — and reading it in one pane says nothing about
// where the other pane happens to be scrolled.
export function getPostElements(postId: string): HTMLElement[] {
    const elements: HTMLElement[] = [];

    const add = (element: HTMLElement | null) => {
        if (element && !elements.includes(element)) {
            elements.push(element);
        }
    };

    // Kept as getElementById so the ids need no CSS escaping.
    add(document.getElementById(`post_${postId}`));
    add(document.getElementById(`rhsPost_${postId}`));

    [
        `.ThreadViewer [data-postid="${postId}"]`,
        `.ThreadViewer [data-post-id="${postId}"]`,
        `[data-postid="${postId}"]`,
        `[data-post-id="${postId}"]`,
    ].forEach((selector) => {
        document.querySelectorAll<HTMLElement>(selector).forEach(add);
    });

    return elements;
}

// The single element the ticks are portalled into; the centre channel wins.
export function getPostElement(postId: string): HTMLElement | null {
    return getPostElements(postId)[0] || null;
}

export function getPostTickAnchor(postId: string): HTMLElement | null {
    const postElement = getPostElement(postId);
    if (!postElement) {
        return null;
    }

    return postElement.querySelector('.post__body') ||
        postElement.querySelector('.post__content');
}

export function getPostIdsInThread(state: {entities: {posts: {postsInThread: Record<string, string[]>}}}, rootId: string): string[] {
    return state.entities.posts.postsInThread[rootId] || [];
}

export function getThreadPostIds(state: ExtendedGlobalState, rootId: string): string[] {
    const postIds = [rootId];
    getPostIdsInThread(state, rootId).forEach((replyId) => {
        postIds.push(replyId);
    });
    return postIds;
}

export function getVisiblePostIds(state: ExtendedGlobalState): string[] {
    const postIds = new Set<string>();
    const activeThreadRootId = getSelectedThreadRootId(state);

    if (activeThreadRootId && isGlobalThreadsViewActive(state)) {
        addThreadPosts(state, postIds, activeThreadRootId);
        return Array.from(postIds);
    }

    const {currentChannelId} = state.entities.channels;

    if (currentChannelId) {
        getPostIdsInChannel(state, currentChannelId).forEach((postId) => {
            postIds.add(postId);

            getPostIdsInThread(state, postId).forEach((replyId) => {
                postIds.add(replyId);
            });
        });
    }

    if (activeThreadRootId) {
        addThreadPosts(state, postIds, activeThreadRootId);
    }

    return Array.from(postIds);
}

export function getVisiblePosts(state: ExtendedGlobalState): Post[] {
    return getVisiblePostIds(state)
        .map((postId) => state.entities.posts.posts[postId])
        .filter(Boolean) as Post[];
}

export function isThreadReply(post: Post): boolean {
    return Boolean(post.root_id);
}

export function isPostInOpenThread(post: Post, rootId?: string): boolean {
    if (!rootId) {
        return false;
    }

    return post.id === rootId || post.root_id === rootId;
}

export function shouldForceReadPost(post: Post, channel: Channel | undefined, openThreadRootId?: string): boolean {
    // Anything in the thread the reader has open counts, including its root.
    // Previously only replies did, so opening someone's message as a thread and
    // answering it left their post unread unless the centre channel happened to
    // still be scrolled to it.
    if (isPostInOpenThread(post, openThreadRootId)) {
        return true;
    }

    if (isThreadReply(post)) {
        return false;
    }

    if (isDirectChannel(channel)) {
        return true;
    }

    return false;
}

export function getReadablePostIds(state: ExtendedGlobalState): string[] {
    const postIds = new Set<string>();
    const activeThreadRootId = getSelectedThreadRootId(state);

    if (activeThreadRootId && isGlobalThreadsViewActive(state)) {
        addThreadPosts(state, postIds, activeThreadRootId);
        return Array.from(postIds);
    }

    const {currentChannelId} = state.entities.channels;

    if (currentChannelId) {
        getPostIdsInChannel(state, currentChannelId).forEach((postId) => {
            postIds.add(postId);
        });
    }

    if (activeThreadRootId) {
        addThreadPosts(state, postIds, activeThreadRootId);
    }

    return Array.from(postIds);
}

export function getReadablePosts(state: ExtendedGlobalState): Post[] {
    return getReadablePostIds(state)
        .map((postId) => state.entities.posts.posts[postId])
        .filter(Boolean) as Post[];
}

export function getPostIdsInChannel(state: {entities: {posts: {postsInChannel: Record<string, Array<{order: string[]}>>}}}, channelId: string): string[] {
    const blocks = state.entities.posts.postsInChannel[channelId];
    if (!blocks?.length) {
        return [];
    }

    const postIds: string[] = [];
    blocks.forEach((block) => {
        if (block?.order?.length) {
            postIds.push(...block.order);
        }
    });

    return postIds;
}

export function getOwnEligiblePostIds(state: ExtendedGlobalState): string[] {
    const {currentUserId} = state.entities.users;
    if (!currentUserId) {
        return [];
    }

    return getVisiblePosts(state)
        .filter((post) => isEligiblePost(post) && isOwnPost(post, currentUserId))
        .map((post) => post.id);
}

export function getPostMessageElement(postId: string): HTMLElement | null {
    const postElement = getPostElement(postId);
    if (!postElement) {
        return null;
    }

    return postElement.querySelector('.post-message__text') ||
        postElement.querySelector('.post-message__text-container') ||
        postElement.querySelector('.post__body');
}
