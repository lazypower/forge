#!/usr/bin/env node
import {initWasm, Resvg} from '@resvg/resvg-wasm';
import {optimize} from 'svgo';
import {readFile, writeFile} from 'node:fs/promises';
import {exit} from 'node:process';

function embedPng(png: Buffer, size: number) {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${size} ${size}"><image width="${size}" height="${size}" href="data:image/png;base64,${png.toString('base64')}"/></svg>`;
}

async function generate(svg: string, path: string, {size, bg}: {size: number, bg?: boolean}) {
  const outputFile = new URL(path, import.meta.url);

  if (outputFile.href.endsWith('.svg')) {
    const {data} = optimize(svg, {
      plugins: [
        'preset-default',
        'removeDimensions',
        {
          name: 'addAttributesToSVGElement',
          params: {
            attributes: [{width: String(size)}, {height: String(size)}],
          },
        },
      ],
    });
    await writeFile(outputFile, data);
    return;
  }

  const resvgJS = new Resvg(svg, {
    fitTo: {
      mode: 'width',
      value: size,
    },
    ...(bg && {background: 'white'}),
  });
  const renderedImage = resvgJS.render();
  const pngBytes = renderedImage.asPng();
  await writeFile(outputFile, Buffer.from(pngBytes));
}

async function main() {
  const logoPng = await readFile(new URL('../assets/logo.png', import.meta.url));
  const faviconPng = await readFile(new URL('../assets/favicon.png', import.meta.url));
  const logoSvg = embedPng(logoPng, 512);
  const faviconSvg = embedPng(faviconPng, 180);
  await initWasm(await readFile(new URL(import.meta.resolve('@resvg/resvg-wasm/index_bg.wasm'))));

  await Promise.all([
    generate(logoSvg, '../public/assets/img/logo.svg', {size: 32}),
    writeFile(new URL('../public/assets/img/logo.png', import.meta.url), logoPng),
    generate(faviconSvg, '../public/assets/img/favicon.svg', {size: 32}),
    writeFile(new URL('../public/assets/img/favicon.png', import.meta.url), faviconPng),
    generate(logoSvg, '../public/assets/img/avatar_default.png', {size: 200}),
    generate(logoSvg, '../public/assets/img/apple-touch-icon.png', {size: 180, bg: true}),
  ]);
}

try {
  await main();
} catch (err) {
  console.error(err);
  exit(1);
}
