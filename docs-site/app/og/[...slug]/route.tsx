import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { ImageResponse } from 'next/og';
import { notFound } from 'next/navigation';
import { source } from '@/lib/source';

// Each page's link preview: the mark, the section, the page's title and
// what it covers, over a quiet line of harbour with its one lamp. Rendered
// at build time; the fonts are static cuts of the site's Inter (the image
// renderer can't read variable woff2).

export const revalidate = false;

const sections: Record<string, string> = {
  'getting-started': 'Getting started',
  concepts: 'Concepts',
  guides: 'Guides',
  reference: 'Reference',
};

const bg = '#1d1e22';
const fg = '#e3e4e8';
const muted = '#8a8c95';
const line = '#5b5e66';
const amber = '#e8a33d';

export async function GET(_req: Request, { params }: { params: Promise<{ slug: string[] }> }) {
  const { slug } = await params;
  const page = source.getPage(slug.slice(0, -1));
  if (!page) notFound();

  const dir = join(process.cwd(), 'assets/og');
  const [regular, semibold] = await Promise.all([readFile(join(dir, 'inter-400.ttf')), readFile(join(dir, 'inter-600.ttf'))]);
  const section = sections[page.slugs[0] ?? ''] ?? 'Documentation';
  const description = page.data.description ?? '';

  return new ImageResponse(
    (
      <div style={{ width: '100%', height: '100%', display: 'flex', flexDirection: 'column', background: bg, padding: '64px 72px', fontFamily: 'Inter' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 18 }}>
          <svg width="52" height="52" viewBox="100 100 824 824">
            <rect x="100" y="100" width="824" height="824" rx="185" fill="#121417" />
            <path d="M304.6 314.5H719.4Q702.1 566.3 512.0 628.0Q321.9 566.3 304.6 314.5Z" fill="none" stroke="#ecebe7" strokeWidth="74.1" strokeLinecap="round" strokeLinejoin="round" /><rect x="366.3" y="694.7" width="71.6" height="54.3" rx="11.1" fill="#ecebe7" /><rect x="476.2" y="694.7" width="71.6" height="54.3" rx="11.1" fill="#ecebe7" /><rect x="586.1" y="694.7" width="71.6" height="54.3" rx="11.1" fill="#ecebe7" />
            <circle cx="512.0" cy="430.5" r="37.0" fill={amber} />
          </svg>
          <div style={{ display: 'flex', fontSize: 30, fontWeight: 600, color: fg }}>
            shipyard<span style={{ color: muted, fontWeight: 400, marginLeft: 10 }}>docs</span>
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', marginTop: 'auto', marginBottom: 'auto', paddingTop: 24 }}>
          <div style={{ fontSize: 24, color: muted }}>{section}</div>
          <div style={{ fontSize: page.data.title.length > 28 ? 60 : 72, fontWeight: 600, color: fg, lineHeight: 1.08, marginTop: 14, letterSpacing: '-0.02em', maxWidth: 1000 }}>{page.data.title}</div>
          {description && (
            <div style={{ fontSize: 28, color: muted, lineHeight: 1.4, marginTop: 22, maxWidth: 940, display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden' }}>{description}</div>
          )}
        </div>

        <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between' }}>
          <svg width="420" height="70" viewBox="0 0 420 70" fill="none">
            <path d="M0 40 H150 M24 40 V62 M66 40 V62 M108 40 V62 M138 40 V30 H156" stroke={line} strokeWidth="3" strokeLinecap="round" />
            <path d="M156 33 C 200 50, 220 52, 252 48" stroke={line} strokeWidth="2" strokeLinecap="round" />
            <path d="M246 48 L 330 48 L 316 62 L 262 62 Z" stroke={line} strokeWidth="3" strokeLinejoin="round" />
            <path d="M286 48 V 22" stroke={line} strokeWidth="3" strokeLinecap="round" />
            <circle cx="286" cy="16" r="7" fill={amber} />
            <path d="M0 68 C 30 64, 60 72, 90 68 S 150 64, 180 68 S 240 72, 270 68 S 330 64, 360 68 S 400 72, 420 68" stroke={line} strokeWidth="2" opacity="0.6" />
          </svg>
          <div style={{ fontSize: 22, color: muted }}>docs.berthd.app</div>
        </div>
      </div>
    ),
    {
      width: 1200,
      height: 630,
      fonts: [
        { name: 'Inter', data: regular, weight: 400, style: 'normal' },
        { name: 'Inter', data: semibold, weight: 600, style: 'normal' },
      ],
    },
  );
}

export function generateStaticParams() {
  return source.getPages().map((page) => ({ slug: [...page.slugs, 'image.png'] }));
}
