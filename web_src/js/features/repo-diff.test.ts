import {GET} from '../modules/fetch.ts';
import {showErrorToast} from '../modules/toast.ts';
import {loadMarkdownPreview} from './repo-diff.ts';

vi.mock('../modules/fetch.ts', () => ({
  GET: vi.fn(),
  POST: vi.fn(),
}));
vi.mock('../modules/toast.ts', () => ({showErrorToast: vi.fn()}));

describe('loadMarkdownPreview', {concurrent: false}, () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(() => vi.restoreAllMocks());

  test('loads and caches rendered content', async () => {
    const preview = document.createElement('div');
    preview.setAttribute('data-markdown-preview-url', '/preview');
    preview.setAttribute('data-markdown-preview-error', 'Preview failed');
    vi.mocked(GET).mockResolvedValue({
      ok: true,
      text: async () => '<div class="markup"><h1>Preview</h1></div>',
    } as Response);

    await loadMarkdownPreview(preview);

    expect(preview.querySelector('h1')?.textContent).toBe('Preview');
    expect(preview.getAttribute('data-markdown-preview-state')).toBe('loaded');
    expect(preview.hasAttribute('aria-busy')).toBe(false);

    await loadMarkdownPreview(preview);
    expect(GET).toHaveBeenCalledOnce();
  });

  test('reports failure and permits retry', async () => {
    const preview = document.createElement('div');
    preview.setAttribute('data-markdown-preview-url', '/preview');
    preview.setAttribute('data-markdown-preview-error', 'Preview failed');
    vi.mocked(GET).mockResolvedValue({ok: false, status: 500, statusText: 'Internal Server Error'} as Response);
    vi.spyOn(console, 'error').mockImplementation(() => {});

    await loadMarkdownPreview(preview);

    expect(preview.textContent).toBe('Preview failed');
    expect(preview.hasAttribute('data-markdown-preview-state')).toBe(false);
    expect(showErrorToast).toHaveBeenCalledWith('Preview failed');

    vi.mocked(GET).mockResolvedValue({ok: true, text: async () => '<p>Retry succeeded</p>'} as Response);
    await loadMarkdownPreview(preview);
    expect(preview.textContent).toBe('Retry succeeded');
    expect(GET).toHaveBeenCalledTimes(2);
  });
});
