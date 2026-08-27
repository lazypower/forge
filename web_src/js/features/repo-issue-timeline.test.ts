import {createElementFromHTML} from '../utils/dom.ts';
import {initRepoIssueTimelineRefresh, refreshIssueTimeline, syncIssueTimelineItems} from './repo-issue-timeline.ts';

describe('issue timeline', {concurrent: false}, () => {
  describe('syncIssueTimelineItems', () => {
    test('InsertNew', () => {
      const oldContent = createElementFromHTML(`
      <div>
        <div class="timeline-item">First</div>
        <div class="timeline-item" id="timeline-comments-end"></div>
      </div>
    `);
      const newContent = createElementFromHTML(`
      <div>
        <div class="timeline-item" id="a">New</div>
      </div>
    `);

      syncIssueTimelineItems(oldContent, newContent);

      expect(oldContent.innerHTML.replace(/>\s+</g, '><').trim()).toBe(
        `<div class="timeline-item">First</div>` +
      `<div class="timeline-item" id="a">New</div>` +
      `<div class="timeline-item" id="timeline-comments-end"></div>`,
      );
    });

    test('Sync', () => {
      const oldContent = createElementFromHTML(`
      <div>
        <div class="timeline-item">First</div>
        <div class="timeline-item" id="it-1">Item 1</div>
        <div class="timeline-item event" id="it-2">Item 2</div>
        <div class="timeline-item" id="it-3">Item 3</div>
        <div class="timeline-item event" id="it-4">Item 4</div>
        <div class="timeline-item" id="timeline-comments-end"></div>
        <div class="timeline-item">Other</div>
      </div>
    `);
      const newContent = createElementFromHTML(`
      <div>
        <div class="timeline-item" id="it-1">New 1</div>
        <div class="timeline-item event" id="it-2">New 2</div>
        <div class="timeline-item" id="it-x">New X</div>
      </div>
    `);

      syncIssueTimelineItems(oldContent, newContent);

      // Item 1 won't be replaced because it's not an event
      // Item 2 will be replaced with New 2
      // Item 3 will be kept because it's not in new content
      // Item 4 will be removed because it's not in new content, and it's an event
      // New X will be inserted at the end of timeline items (before timeline-comments-end)
      expect(oldContent.innerHTML.replace(/>\s+</g, '><').trim()).toBe(
        `<div class="timeline-item">First</div>` +
      `<div class="timeline-item" id="it-1">Item 1</div>` +
      `<div class="timeline-item event" id="it-2">New 2</div>` +
      `<div class="timeline-item" id="it-3">Item 3</div>` +
      `<div class="timeline-item" id="it-x">New X</div>` +
      `<div class="timeline-item" id="timeline-comments-end"></div>` +
      `<div class="timeline-item">Other</div>`,
      );
    });
  });

  describe('refreshIssueTimeline', () => {
    afterEach(() => {
      vi.restoreAllMocks();
    });

    test('fetches and inserts new timeline items', async () => {
      const oldContent = createElementFromHTML(`
      <div class="issue-content-left">
        <div class="timeline-item" id="existing">Existing</div>
        <div class="timeline-item" id="timeline-comments-end"></div>
      </div>
    `);
      const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(`
      <html><body>
        <div class="issue-content-left">
          <div class="timeline-item" id="existing">Existing</div>
          <div class="timeline-item" id="new-comment">Bot feedback</div>
          <div class="timeline-item" id="timeline-comments-end"></div>
        </div>
      </body></html>
    `));

      await refreshIssueTimeline(oldContent, '/owner/repo/pulls/1');

      expect(fetchMock).toHaveBeenCalledWith(expect.stringMatching(/\/owner\/repo\/pulls\/1$/), expect.objectContaining({
        cache: 'no-store',
        method: 'GET',
      }));
      expect(oldContent.querySelector('#new-comment')?.textContent).toBe('Bot feedback');
    });

    test('keeps the timeline unchanged when the refresh fails', async () => {
      const oldContent = createElementFromHTML(`
      <div class="issue-content-left">
        <div class="timeline-item" id="existing">Existing</div>
        <div class="timeline-item" id="timeline-comments-end"></div>
      </div>
    `);
      vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('', {status: 500}));

      await refreshIssueTimeline(oldContent, '/owner/repo/pulls/1');

      expect(oldContent.querySelectorAll('.timeline-item')).toHaveLength(2);
    });
  });

  describe('initRepoIssueTimelineRefresh', () => {
    afterEach(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
      vi.restoreAllMocks();
    });

    test('polls visible pull requests and catches up after the tab becomes visible', async () => {
      vi.useFakeTimers();
      window.history.replaceState({}, '', '/owner/repo/pulls/1');
      document.body.innerHTML = `
      <div class="repository view issue">
        <div class="issue-content-left">
          <div class="timeline-item" id="timeline-comments-end"></div>
        </div>
      </div>
    `;

      let hidden = false;
      vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
      let pendingFetch = Promise.withResolvers<Response>();
      const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => pendingFetch.promise);
      fetchMock.mockClear();
      const response = () => ({
        ok: true,
        text: async () => `
        <html><body>
          <div class="issue-content-left">
            <div class="timeline-item" id="timeline-comments-end"></div>
          </div>
        </body></html>
      `,
      }) as Response;

      initRepoIssueTimelineRefresh();
      vi.advanceTimersByTime(10_000);
      expect(fetchMock).toHaveBeenCalledTimes(1);

      hidden = true;
      document.dispatchEvent(new Event('visibilitychange'));
      pendingFetch.resolve(response());
      await vi.advanceTimersByTimeAsync(0);
      await vi.advanceTimersByTimeAsync(20_000);
      expect(fetchMock).toHaveBeenCalledTimes(1);

      pendingFetch = Promise.withResolvers<Response>();
      hidden = false;
      document.dispatchEvent(new Event('visibilitychange'));
      expect(fetchMock).toHaveBeenCalledTimes(2);
      pendingFetch.resolve(response());
      await vi.advanceTimersByTimeAsync(0);
    });
  });
});
