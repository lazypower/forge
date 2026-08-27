import {GET} from '../modules/fetch.ts';
import {parseDom, parseIssueHref} from '../utils.ts';

const refreshInterval = 10_000;

export function syncIssueTimelineItems(oldMainContent: Element, newMainContent: Element) {
  // find the end of comments timeline by "id=timeline-comments-end" in current main content, and insert new items before it
  const timelineEnd = oldMainContent.querySelector('.timeline-item[id="timeline-comments-end"]');
  if (!timelineEnd) return;

  const oldTimelineItems = oldMainContent.querySelectorAll(`.timeline-item[id]`);
  for (const oldItem of oldTimelineItems) {
    const oldItemId = oldItem.getAttribute('id')!;
    const newItem = newMainContent.querySelector(`.timeline-item[id="${CSS.escape(oldItemId)}"]`);
    if (oldItem.classList.contains('event') && !newItem) {
      // if the item is not in new content, we want to remove it from old content only if it's an event item, otherwise we keep it
      oldItem.remove();
    }
  }

  const newTimelineItems = newMainContent.querySelectorAll(`.timeline-item[id]`);
  for (const newItem of newTimelineItems) {
    const newItemId = newItem.getAttribute('id')!;
    const oldItem = oldMainContent.querySelector(`.timeline-item[id="${CSS.escape(newItemId)}"]`);
    if (oldItem) {
      if (oldItem.classList.contains('event')) {
        // for event item (e.g.: "add & remove labels"), we want to replace the existing one if exists
        // because the label operations can be merged into one event item, so the new item might be different from the old one
        oldItem.replaceWith(newItem);
      }
      continue;
    }
    timelineEnd.insertAdjacentElement('beforebegin', newItem);
  }
}

export async function refreshIssueTimeline(oldMainContent: Element, pageUrl = window.location.href) {
  const resp = await GET(pageUrl, {cache: 'no-store'});
  if (!resp.ok) return;

  const doc = parseDom(await resp.text(), 'text/html');
  const newMainContent = doc.querySelector('.issue-content-left');
  if (!newMainContent) return;

  syncIssueTimelineItems(oldMainContent, newMainContent);
}

export function initRepoIssueTimelineRefresh() {
  if (parseIssueHref(window.location.href).pathType !== 'pulls') return;

  const mainContent = document.querySelector('.repository.view.issue .issue-content-left');
  if (!mainContent?.querySelector('#timeline-comments-end')) return;

  let timerId: number | null = null;
  let refreshing = false;
  let runRefresh: () => Promise<void>;

  const stopRefresh = () => {
    if (timerId === null) return;
    window.clearTimeout(timerId);
    timerId = null;
  };

  const scheduleRefresh = () => {
    if (document.hidden || timerId !== null) return;
    timerId = window.setTimeout(runRefresh, refreshInterval);
  };

  runRefresh = async () => {
    timerId = null;
    if (document.hidden || refreshing) return;

    refreshing = true;
    try {
      await refreshIssueTimeline(mainContent);
    } catch (error) {
      console.error('Unable to refresh issue timeline', error);
    }
    refreshing = false;
    scheduleRefresh();
  };

  const onVisibilityChange = () => {
    stopRefresh();
    if (!document.hidden) runRefresh();
  };

  document.addEventListener('visibilitychange', onVisibilityChange);
  scheduleRefresh();
}
