import { useCallback, useEffect, useMemo, useState } from 'react';
import { Api } from '../api';

// BranchMap — staff geospatial view (GEOSPATIAL tranche).
//
// "Geolibre" interpretation: the libre/open-source mapping stack is
// MapLibre GL + OpenStreetMap tiles. maplibre-gl is NOT yet in
// apps/staff-portal/package.json, so this view deliberately renders WITHOUT
// adding a build dependency: an SVG schematic of Nigeria (lat 4..14,
// lng 2..15 bounding box) with branch markers and a click-to-query
// nearest-branch tool, powered by /api/v1/geo/branches/nearest and
// /api/v1/geo/coverage. To upgrade to real tiles later:
//   npm i maplibre-gl && render <maplibregl.Map> with an OSM raster style —
//   the data plumbing below (fetchers, marker model) carries over as-is.

interface GeoBranch {
  id: string; name: string; state: string; city: string;
  lat: number; lng: number;
}
interface BranchDistance { branch: GeoBranch; distance_km: number }
interface StateCoverage { state: string; branches: number; has_geo: number }

// Nigeria bounding box used server-side for data-quality soft-flagging.
const NG = { minLat: 4, maxLat: 14, minLng: 2, maxLng: 15 };

// Rough simplified Nigeria outline (placeholder; replace with a real
// boundary GeoJSON or MapLibre tiles when maplibre-gl is added).
const NG_OUTLINE: [number, number][] = [ // [lng, lat]
  [2.7, 6.3], [3.0, 6.4], [3.6, 6.4], [4.3, 6.1], [4.5, 5.8], [5.0, 4.7],
  [6.0, 4.4], [7.0, 4.4], [8.0, 4.6], [8.5, 4.8], [8.5, 5.5], [9.2, 5.9],
  [9.6, 6.4], [10.0, 6.8], [10.5, 7.0], [11.5, 6.8], [12.5, 7.5], [13.5, 8.0],
  [14.6, 9.6], [14.6, 11.5], [14.0, 12.4], [13.8, 13.3], [13.3, 13.7],
  [12.0, 13.6], [10.0, 13.4], [8.0, 13.0], [6.5, 13.0], [4.5, 12.5],
  [3.5, 11.7], [3.0, 11.5], [2.8, 10.0], [2.7, 8.0], [2.7, 6.3],
];

const W = 720, H = 560, PAD = 16;
const x = (lng: number) => PAD + ((lng - NG.minLng) / (NG.maxLng - NG.minLng)) * (W - 2 * PAD);
const y = (lat: number) => H - PAD - ((lat - NG.minLat) / (NG.maxLat - NG.minLat)) * (H - 2 * PAD);
const inv = (px: number, py: number): [number, number] => [ // [lat, lng]
  NG.minLat + ((H - PAD - py) / (H - 2 * PAD)) * (NG.maxLat - NG.minLat),
  NG.minLng + ((px - PAD) / (W - 2 * PAD)) * (NG.maxLng - NG.minLng),
];

export default function BranchMap({ api }: { api: Api }) {
  const [coverage, setCoverage] = useState<StateCoverage[]>([]);
  const [nearest, setNearest] = useState<BranchDistance[]>([]);
  const [pin, setPin] = useState<[number, number] | null>(null); // [lat, lng]
  const [outsideNg, setOutsideNg] = useState(false);
  const [err, setErr] = useState('');

  // The geo endpoints are not yet on the typed Api client (api.ts is a
  // shared file); call them directly with the same auth/tenant headers.
  const get = useCallback(async (path: string) => {
    const res = await fetch(api.base + path, {
      headers: {
        'Content-Type': 'application/json',
        'X-Tenant-ID': api.tenantId,
        ...(api.token ? { Authorization: `Bearer ${api.token}` } : {}),
      },
    });
    if (!res.ok) throw new Error(`${res.status}: ${await res.text()}`);
    return res;
  }, [api]);

  useEffect(() => {
    get('/api/v1/geo/coverage')
      .then(r => r.json())
      .then((body: { states: StateCoverage[] }) => { setCoverage(body.states); setErr(''); })
      .catch((e: Error) => setErr(e.message));
  }, [get]);

  const queryNearest = useCallback(async (lat: number, lng: number) => {
    try {
      const r = await get(`/api/v1/geo/branches/nearest?lat=${lat}&lng=${lng}&k=10`);
      setNearest(await r.json());
      setOutsideNg(r.headers.get('X-Geo-Outside-Nigeria') === '1');
      setErr('');
    } catch (e: any) { setErr(e.message); }
  }, [get]);

  const onMapClick = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const [lat, lng] = inv(
      (e.clientX - rect.left) * (W / rect.width),
      (e.clientY - rect.top) * (H / rect.height),
    );
    setPin([lat, lng]);
    queryNearest(lat, lng);
  };

  const markers = useMemo(() => {
    const ranked = new Map(nearest.map((d, i) => [d.branch.id, i]));
    const merged: { b: GeoBranch; rank?: number; km?: number }[] = [];
    for (const d of nearest) {
      if (d.branch.lat || d.branch.lng) {
        merged.push({ b: d.branch, rank: ranked.get(d.branch.id), km: d.distance_km });
      }
    }
    return merged;
  }, [nearest]);

  const totalBranches = coverage.reduce((s, c) => s + c.branches, 0);
  const geoBranches = coverage.reduce((s, c) => s + c.has_geo, 0);
  const gapStates = coverage.filter(c => c.branches === 0 || c.has_geo < c.branches);

  return (
    <section>
      <h2>Branch map &amp; coverage</h2>
      <p style={{ color: '#6b7280' }}>
        Schematic SVG map (MapLibre GL + OSM tiles can replace this when maplibre-gl is added —
        see docs/GEOSPATIAL.md). Click anywhere to find the nearest branches.
      </p>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}

      <svg viewBox={`0 0 ${W} ${H}`} onClick={onMapClick}
        style={{ width: '100%', maxWidth: W, background: '#eff6ff', border: '1px solid #d1d5db', borderRadius: 8, cursor: 'crosshair' }}>
        <polygon
          points={NG_OUTLINE.map(([lng, lat]) => `${x(lng)},${y(lat)}`).join(' ')}
          fill="#f0fdf4" stroke="#16a34a" strokeWidth={1.5} />
        {markers.map(({ b, rank }) => (
          <g key={b.id}>
            <circle cx={x(b.lng)} cy={y(b.lat)} r={rank === 0 ? 7 : 4}
              fill={rank === 0 ? '#dc2626' : '#2563eb'} stroke="#fff" strokeWidth={1.5}>
              <title>{b.name} — {b.city}, {b.state}</title>
            </circle>
            {rank !== undefined && (
              <text x={x(b.lng) + 8} y={y(b.lat) - 6} fontSize={11} fill="#111827">
                {rank + 1}. {b.name}
              </text>
            )}
          </g>
        ))}
        {pin && (
          <circle cx={x(pin[1])} cy={y(pin[0])} r={5} fill="none" stroke="#dc2626" strokeWidth={2} strokeDasharray="3 2" />
        )}
      </svg>
      {outsideNg && <p style={{ color: '#b45309' }}>Query point is outside Nigeria — results may be sparse (diaspora mode).</p>}

      {nearest.length > 0 && (
        <>
          <h3>Nearest branches {pin && `(${pin[0].toFixed(3)}, ${pin[1].toFixed(3)})`}</h3>
          <table style={{ borderCollapse: 'collapse', width: '100%', marginBottom: 16 }}>
            <thead><tr><Th>#</Th><Th>Branch</Th><Th>State</Th><Th>Distance</Th></tr></thead>
            <tbody>
              {nearest.map((d, i) => (
                <tr key={d.branch.id}>
                  <Td>{i + 1}</Td>
                  <Td>{d.branch.name}</Td>
                  <Td>{d.branch.state}</Td>
                  <Td>{d.distance_km.toFixed(1)} km</Td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      <h3>Coverage by state</h3>
      <p>
        {geoBranches}/{totalBranches} branches geocoded
        {gapStates.length > 0 && <> — gaps in: {gapStates.map(s => s.state).join(', ')}</>}
      </p>
      <table style={{ borderCollapse: 'collapse', width: '100%' }}>
        <thead><tr><Th>State</Th><Th>Branches</Th><Th>Geocoded</Th></tr></thead>
        <tbody>
          {coverage.map(c => (
            <tr key={c.state} style={c.has_geo < c.branches ? { background: '#fef2f2' } : undefined}>
              <Td>{c.state}</Td><Td>{c.branches}</Td><Td>{c.has_geo}</Td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

const Th = ({ children }: { children: React.ReactNode }) => (
  <th style={{ textAlign: 'left', borderBottom: '2px solid #e5e7eb', padding: '4px 8px' }}>{children}</th>
);
const Td = ({ children }: { children: React.ReactNode }) => (
  <td style={{ borderBottom: '1px solid #f3f4f6', padding: '4px 8px' }}>{children}</td>
);
