import React from 'react';

type Props = {
    status: 'delivered' | 'read';
    label: string;
    title: string;
    size: number;
};

// Geometry traced from Material's "done_all" glyph, which is the shape people
// recognise as a read receipt: the read state is two overlapping checks, and the
// rear one is interrupted where the front one crosses it rather than simply
// sitting beside it. The coordinates below are stroke centrelines in that
// icon's 24-unit grid (Material ships it as a filled outline; these are the
// midlines of its 2-unit-wide strokes), so the glyph stays crisp at any size
// instead of being scaled from a filled path.
const CHECK_SINGLE = 'M1.12 12.71L6 17.59L17.29 6.3';
const CHECK_REAR_STUB = 'M1.12 12.71L6.71 18.3';
const CHECK_REAR_ARM = 'M10.96 12.64L17.3 6.3';
const CHECK_FRONT = 'M6.78 12.71L11.66 17.59L22.95 6.3';

// Both glyphs sit on the same baseline, so the viewBoxes crop the same 15-unit
// band and differ only in width. That keeps the two states vertically aligned
// when a post flips from delivered to read.
const VIEWBOX_Y = 5;
const VIEWBOX_HEIGHT = 15;
const VIEWBOX_WIDTH = {delivered: 18.3, read: 24};
const STROKE_WIDTH = 2;

// A stroke of 2 viewBox units renders thinner and thinner as the glyph shrinks
// — at 6px it is 0.8 device px, which is a grey smudge rather than a tick. Below
// roughly 8px the stroke is widened to keep about a pixel of ink.
const MIN_STROKE_PX = 1.1;

function strokeWidth(size: number): number {
    return Math.max(STROKE_WIDTH, (MIN_STROKE_PX * VIEWBOX_HEIGHT) / size);
}

const MessageStatusTicks: React.FC<Props> = ({status, label, title, size}) => {
    const isRead = status === 'read';
    const paths = isRead ? [CHECK_REAR_STUB, CHECK_REAR_ARM, CHECK_FRONT] : [CHECK_SINGLE];
    const viewBoxWidth = VIEWBOX_WIDTH[status];
    const stroke = strokeWidth(size);

    return (
        <span
            className={`message-status-ticks message-status-ticks--${status}`}
            aria-label={label}
            title={title}
        >
            <svg
                className='message-status-ticks__icon'
                width={(size * viewBoxWidth) / VIEWBOX_HEIGHT}
                height={size}
                viewBox={`0 ${VIEWBOX_Y} ${viewBoxWidth} ${VIEWBOX_HEIGHT}`}
                role='img'
                aria-hidden='true'
                focusable='false'
            >
                {paths.map((d) => (
                    <path
                        key={d}
                        d={d}
                        fill='none'
                        stroke='currentColor'
                        strokeWidth={stroke}
                        strokeLinecap='round'
                        strokeLinejoin='round'
                    />
                ))}
            </svg>
        </span>
    );
};

export default MessageStatusTicks;
