export type MapLandmark = {
  id: string;
  nameZh: string;
  x: number;
  y: number;
  kind: 'fast_travel' | 'boss_tower';
};

export const DEFAULT_LANDMARK_RADIUS = 25_000;

// Provenance: world coordinates aligned with zaigie/palworld-server-tool
// web/src/assets/map/points.json (fast_travel + boss_tower).
// Source URL (fetched for this table):
// https://raw.githubusercontent.com/zaigie/palworld-server-tool/main/web/src/assets/map/points.json
// Chinese names from community map labels; unnamed entries use 传送点/首领塔 + index
// (v1 fallback per timeline-map-ux-polish design).
// Counts: 137 fast_travel + 9 boss_tower = 146.
export const MAP_LANDMARKS: MapLandmark[] = [
  { id: "ft-0", nameZh: "传送点 1", x: -108666.8, y: 79119.9, kind: "fast_travel" },
  { id: "ft-1", nameZh: "传送点 2", x: -265220, y: 173530, kind: "fast_travel" },
  { id: "ft-2", nameZh: "传送点 3", x: -327531.8, y: -70410.6, kind: "fast_travel" },
  { id: "ft-3", nameZh: "传送点 4", x: -367707.8, y: -114950.9, kind: "fast_travel" },
  { id: "ft-4", nameZh: "传送点 5", x: -414446.7, y: -24603.3, kind: "fast_travel" },
  { id: "ft-5", nameZh: "传送点 6", x: -465895, y: -62137.8, kind: "fast_travel" },
  { id: "ft-6", nameZh: "传送点 7", x: -416557.5, y: -90012.2, kind: "fast_travel" },
  { id: "ft-7", nameZh: "传送点 8", x: -383183.2, y: -210790.9, kind: "fast_travel" },
  { id: "ft-8", nameZh: "传送点 9", x: 29780.5, y: 406913.2, kind: "fast_travel" },
  { id: "ft-9", nameZh: "传送点 10", x: -50301, y: 287392.4, kind: "fast_travel" },
  { id: "ft-10", nameZh: "传送点 11", x: 35588.7, y: 321331.2, kind: "fast_travel" },
  { id: "ft-11", nameZh: "传送点 12", x: 117624.2, y: 400134.8, kind: "fast_travel" },
  { id: "ft-12", nameZh: "传送点 13", x: -119037.3, y: 138340.4, kind: "fast_travel" },
  { id: "ft-13", nameZh: "传送点 14", x: -80506.2, y: 141464.8, kind: "fast_travel" },
  { id: "ft-14", nameZh: "传送点 15", x: -39402.4, y: 159611.9, kind: "fast_travel" },
  { id: "ft-15", nameZh: "传送点 16", x: 8000.5, y: 111770.6, kind: "fast_travel" },
  { id: "ft-16", nameZh: "传送点 17", x: 102159.8, y: 48527, kind: "fast_travel" },
  { id: "ft-17", nameZh: "传送点 18", x: 89625, y: 94363.3, kind: "fast_travel" },
  { id: "ft-18", nameZh: "传送点 19", x: -777490, y: -40589, kind: "fast_travel" },
  { id: "ft-19", nameZh: "传送点 20", x: -758105, y: -61810, kind: "fast_travel" },
  { id: "ft-20", nameZh: "传送点 21", x: -818013.1, y: 59827.7, kind: "fast_travel" },
  { id: "ft-21", nameZh: "传送点 22", x: -771309.9, y: 8647.2, kind: "fast_travel" },
  { id: "ft-22", nameZh: "传送点 23", x: -829095.7, y: -19262.8, kind: "fast_travel" },
  { id: "ft-23", nameZh: "传送点 24", x: -811466.6, y: -85874.3, kind: "fast_travel" },
  { id: "ft-24", nameZh: "传送点 25", x: -237086.6, y: -100489.9, kind: "fast_travel" },
  { id: "ft-25", nameZh: "传送点 26", x: -798776.8, y: -479574.8, kind: "fast_travel" },
  { id: "ft-26", nameZh: "传送点 27", x: -117215, y: 446690, kind: "fast_travel" },
  { id: "ft-27", nameZh: "传送点 28", x: -63520.7, y: -55005.9, kind: "fast_travel" },
  { id: "ft-28", nameZh: "传送点 29", x: -86709.6, y: -140441.8, kind: "fast_travel" },
  { id: "ft-29", nameZh: "传送点 30", x: -25522.2, y: -118365.3, kind: "fast_travel" },
  { id: "ft-30", nameZh: "传送点 31", x: -25915.4, y: -78926.2, kind: "fast_travel" },
  { id: "ft-31", nameZh: "传送点 32", x: 32787.1, y: -81420.3, kind: "fast_travel" },
  { id: "ft-32", nameZh: "传送点 33", x: -2188.8, y: -148545.9, kind: "fast_travel" },
  { id: "ft-33", nameZh: "传送点 34", x: -503990, y: -214380, kind: "fast_travel" },
  { id: "ft-34", nameZh: "传送点 35", x: -588140, y: -253780, kind: "fast_travel" },
  { id: "ft-35", nameZh: "传送点 36", x: -792350, y: -251110, kind: "fast_travel" },
  { id: "ft-36", nameZh: "传送点 37", x: -698514.9, y: -322880.4, kind: "fast_travel" },
  { id: "ft-37", nameZh: "传送点 38", x: -760285.4, y: -354966.2, kind: "fast_travel" },
  { id: "ft-38", nameZh: "传送点 39", x: -888080, y: -433080, kind: "fast_travel" },
  { id: "ft-39", nameZh: "传送点 40", x: -699510, y: -389030, kind: "fast_travel" },
  { id: "ft-40", nameZh: "传送点 41", x: -591120, y: -484260, kind: "fast_travel" },
  { id: "ft-41", nameZh: "传送点 42", x: -671901.2, y: -172407.6, kind: "fast_travel" },
  { id: "ft-42", nameZh: "传送点 43", x: -572461.6, y: -287224.1, kind: "fast_travel" },
  { id: "ft-43", nameZh: "传送点 44", x: -130060.4, y: -53406.7, kind: "fast_travel" },
  { id: "ft-44", nameZh: "传送点 45", x: -250506.5, y: 341109.6, kind: "fast_travel" },
  { id: "ft-45", nameZh: "传送点 46", x: 168489, y: 221797.6, kind: "fast_travel" },
  { id: "ft-46", nameZh: "传送点 47", x: -33882.2, y: 564292.5, kind: "fast_travel" },
  { id: "ft-47", nameZh: "传送点 48", x: -197915.2, y: 438001.4, kind: "fast_travel" },
  { id: "ft-48", nameZh: "传送点 49", x: -335853.8, y: 335361.3, kind: "fast_travel" },
  { id: "ft-49", nameZh: "传送点 50", x: -558410.4, y: 121112.1, kind: "fast_travel" },
  { id: "ft-50", nameZh: "传送点 51", x: -488718.2, y: -35724.9, kind: "fast_travel" },
  { id: "ft-51", nameZh: "传送点 52", x: -209326, y: -249227.6, kind: "fast_travel" },
  { id: "ft-52", nameZh: "传送点 53", x: 14388.2, y: 476545.6, kind: "fast_travel" },
  { id: "ft-53", nameZh: "传送点 54", x: -71914.8, y: 472172.2, kind: "fast_travel" },
  { id: "ft-54", nameZh: "传送点 55", x: -38212, y: 406147.1, kind: "fast_travel" },
  { id: "ft-55", nameZh: "传送点 56", x: -105662.5, y: 400797, kind: "fast_travel" },
  { id: "ft-56", nameZh: "传送点 57", x: 191211.2, y: 375334.7, kind: "fast_travel" },
  { id: "ft-57", nameZh: "传送点 58", x: 178736.6, y: 305772.4, kind: "fast_travel" },
  { id: "ft-58", nameZh: "传送点 59", x: 125988.7, y: 274597.8, kind: "fast_travel" },
  { id: "ft-59", nameZh: "传送点 60", x: 12156.8, y: 249201.4, kind: "fast_travel" },
  { id: "ft-60", nameZh: "传送点 61", x: 69604.1, y: 196989.3, kind: "fast_travel" },
  { id: "ft-61", nameZh: "传送点 62", x: 107940.7, y: -28036.3, kind: "fast_travel" },
  { id: "ft-62", nameZh: "传送点 63", x: -38097.3, y: 53412.5, kind: "fast_travel" },
  { id: "ft-63", nameZh: "传送点 64", x: -43926.7, y: -173601, kind: "fast_travel" },
  { id: "ft-64", nameZh: "传送点 65", x: -131353.9, y: 54481.7, kind: "fast_travel" },
  { id: "ft-65", nameZh: "传送点 66", x: -160345.2, y: 98540.8, kind: "fast_travel" },
  { id: "ft-66", nameZh: "传送点 67", x: 63442.5, y: 507395.4, kind: "fast_travel" },
  { id: "ft-67", nameZh: "传送点 68", x: -153135.8, y: 589409.5, kind: "fast_travel" },
  { id: "ft-68", nameZh: "传送点 69", x: -451245, y: 363338.2, kind: "fast_travel" },
  { id: "ft-69", nameZh: "传送点 70", x: -984033.7, y: -371189.8, kind: "fast_travel" },
  { id: "ft-70", nameZh: "传送点 71", x: -568698.6, y: -603451.9, kind: "fast_travel" },
  { id: "ft-71", nameZh: "传送点 72", x: -426910.2, y: -436850.9, kind: "fast_travel" },
  { id: "ft-72", nameZh: "传送点 73", x: -61819, y: -246053.5, kind: "fast_travel" },
  { id: "ft-73", nameZh: "传送点 74", x: -301681.9, y: -5184.1, kind: "fast_travel" },
  { id: "ft-74", nameZh: "传送点 75", x: -266957.5, y: 93228.6, kind: "fast_travel" },
  { id: "ft-75", nameZh: "传送点 76", x: -236840.5, y: 34772.6, kind: "fast_travel" },
  { id: "ft-76", nameZh: "传送点 77", x: -311508.9, y: 76832.3, kind: "fast_travel" },
  { id: "ft-77", nameZh: "传送点 78", x: -421434.3, y: 29307.7, kind: "fast_travel" },
  { id: "ft-78", nameZh: "传送点 79", x: -259198.7, y: -59353.4, kind: "fast_travel" },
  { id: "ft-79", nameZh: "传送点 80", x: -287201.4, y: -217569, kind: "fast_travel" },
  { id: "ft-80", nameZh: "传送点 81", x: -415272.2, y: -162408.6, kind: "fast_travel" },
  { id: "ft-81", nameZh: "传送点 82", x: -119900.9, y: 351109.2, kind: "fast_travel" },
  { id: "ft-82", nameZh: "传送点 83", x: -156482.2, y: 317415.3, kind: "fast_travel" },
  { id: "ft-83", nameZh: "传送点 84", x: -224958.1, y: 285919.2, kind: "fast_travel" },
  { id: "ft-84", nameZh: "传送点 85", x: -74928.6, y: 212787.1, kind: "fast_travel" },
  { id: "ft-85", nameZh: "传送点 86", x: -578061, y: -158216.1, kind: "fast_travel" },
  { id: "ft-86", nameZh: "传送点 87", x: -509887.2, y: -299269, kind: "fast_travel" },
  { id: "ft-87", nameZh: "传送点 88", x: -506737.8, y: -396063.4, kind: "fast_travel" },
  { id: "ft-88", nameZh: "传送点 89", x: -603491.1, y: -338965.2, kind: "fast_travel" },
  { id: "ft-89", nameZh: "传送点 90", x: -651722.9, y: -373365.2, kind: "fast_travel" },
  { id: "ft-90", nameZh: "传送点 91", x: -644486, y: -276536.5, kind: "fast_travel" },
  { id: "ft-91", nameZh: "传送点 92", x: -725188.8, y: -238441.9, kind: "fast_travel" },
  { id: "ft-92", nameZh: "传送点 93", x: -714540.9, y: -458346.6, kind: "fast_travel" },
  { id: "ft-93", nameZh: "传送点 94", x: -771223.4, y: -441798.3, kind: "fast_travel" },
  { id: "ft-94", nameZh: "传送点 95", x: -810648.4, y: -393963.7, kind: "fast_travel" },
  { id: "ft-95", nameZh: "传送点 96", x: -865524.3, y: -352355, kind: "fast_travel" },
  { id: "ft-96", nameZh: "传送点 97", x: -923275.1, y: -385109.1, kind: "fast_travel" },
  { id: "ft-97", nameZh: "传送点 98", x: -886234.2, y: -483745.9, kind: "fast_travel" },
  { id: "ft-98", nameZh: "传送点 99", x: 192505.8, y: -227006.5, kind: "fast_travel" },
  { id: "ft-99", nameZh: "传送点 100", x: -294830, y: 152240, kind: "fast_travel" },
  { id: "ft-100", nameZh: "传送点 101", x: -319000, y: 127170, kind: "fast_travel" },
  { id: "ft-101", nameZh: "传送点 102", x: -137210, y: -91340, kind: "fast_travel" },
  { id: "ft-102", nameZh: "传送点 103", x: -88645.9, y: -3922.9, kind: "fast_travel" },
  { id: "ft-103", nameZh: "传送点 104", x: -170400, y: -29240, kind: "fast_travel" },
  { id: "ft-104", nameZh: "传送点 105", x: -450320, y: 112630, kind: "fast_travel" },
  { id: "ft-105", nameZh: "传送点 106", x: -376750, y: 124630, kind: "fast_travel" },
  { id: "ft-106", nameZh: "传送点 107", x: -374180, y: 63960, kind: "fast_travel" },
  { id: "ft-107", nameZh: "传送点 108", x: -265980, y: 268690, kind: "fast_travel" },
  { id: "ft-108", nameZh: "传送点 109", x: -282110, y: 355570, kind: "fast_travel" },
  { id: "ft-109", nameZh: "传送点 110", x: -170718.7, y: 409753.2, kind: "fast_travel" },
  { id: "ft-110", nameZh: "传送点 111", x: -221399.3, y: 330684.5, kind: "fast_travel" },
  { id: "ft-111", nameZh: "传送点 112", x: -248770.1, y: 126205.8, kind: "fast_travel" },
  { id: "ft-112", nameZh: "传送点 113", x: -338560, y: 107660, kind: "fast_travel" },
  { id: "ft-113", nameZh: "传送点 114", x: -215388.5, y: 8854.2, kind: "fast_travel" },
  { id: "ft-114", nameZh: "传送点 115", x: -195236.2, y: 36718.5, kind: "fast_travel" },
  { id: "ft-115", nameZh: "传送点 116", x: 6, y: 0, kind: "fast_travel" },
  { id: "ft-116", nameZh: "传送点 117", x: -326403.8, y: 55136.7, kind: "fast_travel" },
  { id: "ft-117", nameZh: "传送点 118", x: -349535.5, y: -4035.1, kind: "fast_travel" },
  { id: "ft-118", nameZh: "传送点 119", x: -283781, y: 59352.7, kind: "fast_travel" },
  { id: "ft-119", nameZh: "传送点 120", x: -218636.8, y: 58528.1, kind: "fast_travel" },
  { id: "ft-120", nameZh: "传送点 121", x: -257951.4, y: 151247.8, kind: "fast_travel" },
  { id: "ft-121", nameZh: "传送点 122", x: -167684.9, y: 167519.2, kind: "fast_travel" },
  { id: "ft-122", nameZh: "传送点 123", x: -221230, y: 165130, kind: "fast_travel" },
  { id: "ft-123", nameZh: "传送点 124", x: -237610, y: 190610, kind: "fast_travel" },
  { id: "ft-124", nameZh: "传送点 125", x: -151842, y: 213101.5, kind: "fast_travel" },
  { id: "ft-125", nameZh: "传送点 126", x: -125476, y: 201720, kind: "fast_travel" },
  { id: "ft-126", nameZh: "传送点 127", x: -103435, y: 234761.2, kind: "fast_travel" },
  { id: "ft-127", nameZh: "传送点 128", x: -115430, y: 289090, kind: "fast_travel" },
  { id: "ft-128", nameZh: "传送点 129", x: -177770, y: 265010, kind: "fast_travel" },
  { id: "ft-129", nameZh: "传送点 130", x: -219172.9, y: 226394.8, kind: "fast_travel" },
  { id: "ft-130", nameZh: "传送点 131", x: -317777.2, y: 212103.3, kind: "fast_travel" },
  { id: "ft-131", nameZh: "传送点 132", x: -342035, y: 236885, kind: "fast_travel" },
  { id: "ft-132", nameZh: "传送点 133", x: -314840, y: 186850, kind: "fast_travel" },
  { id: "ft-133", nameZh: "传送点 134", x: -358785, y: 267940, kind: "fast_travel" },
  { id: "ft-134", nameZh: "传送点 135", x: -346617.6, y: 191706.6, kind: "fast_travel" },
  { id: "ft-135", nameZh: "传送点 136", x: -278468, y: 212679.4, kind: "fast_travel" },
  { id: "ft-136", nameZh: "传送点 137", x: -302825, y: 241060, kind: "fast_travel" },
  { id: "tw-0", nameZh: "西原之塔", x: -266563.2, y: 174506.3, kind: "boss_tower" },
  { id: "tw-1", nameZh: "竹林之塔", x: -361695, y: -112009, kind: "boss_tower" },
  { id: "tw-2", nameZh: "湖心之塔", x: 81363, y: 90183, kind: "boss_tower" },
  { id: "tw-3", nameZh: "冰封之塔", x: 29975.3, y: 413325, kind: "boss_tower" },
  { id: "tw-4", nameZh: "荒漠之塔", x: -321596.2, y: 209085, kind: "boss_tower" },
  { id: "tw-5", nameZh: "雷鸣之塔", x: -778215.9, y: -36026, kind: "boss_tower" },
  { id: "tw-6", nameZh: "火山之塔", x: -889805.2, y: -435828, kind: "boss_tower" },
  { id: "tw-7", nameZh: "南境之塔", x: -29427.6, y: -115900.1, kind: "boss_tower" },
  { id: "tw-8", nameZh: "初始之塔", x: -108093.8, y: 77936.1, kind: "boss_tower" },
];

/** Coarse regional label for FT points (best-effort; not official POI names). */
export function regionNameZh(x: number, y: number): string {
  if (y >= 280_000) return '雪原';
  if (y <= -280_000) return '火山带';
  if (x <= -700_000) return '远西';
  if (x >= 50_000 && y >= 50_000) return '东北';
  if (x >= 50_000) return '东陆';
  if (x <= -350_000 && y >= 0) return '西陆';
  if (x <= -350_000) return '西南';
  if (y >= 80_000) return '北陆';
  if (y <= -80_000) return '南陆';
  return '中央';
}

// Enrich generic FT labels with region for better Chinese readability.
for (const lm of MAP_LANDMARKS) {
  if (lm.kind === 'fast_travel' && /^传送点 \d+$/.test(lm.nameZh)) {
    lm.nameZh = `${regionNameZh(lm.x, lm.y)} · ${lm.nameZh}`;
  }
}

export function nearestLandmark(
  point: { x: number; y: number },
  landmarks: MapLandmark[] = MAP_LANDMARKS,
  radius = DEFAULT_LANDMARK_RADIUS,
): MapLandmark | undefined {
  let best: MapLandmark | undefined;
  let bestDist = Infinity;
  for (const lm of landmarks) {
    const dx = lm.x - point.x;
    const dy = lm.y - point.y;
    const d = Math.hypot(dx, dy);
    if (d > radius) continue;
    if (d < bestDist || (d === bestDist && (!best || lm.id < best.id))) {
      best = lm;
      bestDist = d;
    }
  }
  return best;
}

export function knownMapLocation(
  sample: { x: number; y: number },
  landmarks: MapLandmark[] = MAP_LANDMARKS,
  radius = DEFAULT_LANDMARK_RADIUS,
): string {
  const hit = nearestLandmark(sample, landmarks, radius);
  return hit ? `靠近 · ${hit.nameZh}` : '';
}

