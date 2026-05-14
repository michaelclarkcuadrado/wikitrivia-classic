export function createWikimediaImage(image: string, width = 300): string {
  return `https://commons.wikimedia.org/w/index.php?title=Special:Redirect/file/${encodeURIComponent(
    image
  )}&width=${width}`;
}

function hashString(str: string): number {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = (hash << 5) - hash + str.charCodeAt(i);
    hash |= 0;
  }
  return Math.abs(hash);
}

export function fallbackGradient(title: string): string {
  const hash = hashString(title);
  const hue1 = hash % 360;
  const hue2 = (hue1 + 25 + ((hash >> 8) % 30)) % 360;
  const angle = (hash >> 4) % 360;
  return `linear-gradient(${angle}deg, hsl(${hue1}, 14%, 42%), hsl(${hue2}, 16%, 28%))`;
}
