// Holder ("collector") badge — a fun, deterministic label derived from a
// wallet address. Pure frontend: hash(lowercased address) % list.length.
// Anime-hybrid names per the product spec. No on-chain or backend meaning;
// purely cosmetic labeling for the current owner/holder of an NFT.

export const HOLDER_NAMES = [
  'SharinganHodl', 'RasenganMint', 'HokageWhale', 'PirateKingHodl',
  'DevilFruitMint', 'SuperSaiyanHodl', 'UltraInstinctWhale', 'HashiraHodl',
  'DomainExpansionMint', 'QuirkHodl', 'WaifuWhale', 'OtakuOG', 'RasenganRekt',
  'KamehamehaHodl', 'ChidoriMint', 'GomuGomuWhale', 'BankaiHodl',
  'ZanpakutoMint', 'TitanSlayerHodl', 'OdmGearWhale', 'AlchemistMint',
  'PhilosopherHodl', 'SusanooWhale', 'ByakuganMint', 'RinneganHodl',
  'NineTailsWhale', 'AkatsukiMint', 'JutsuHodl', 'SenpaiWhale', 'KawaiiMint',
  'MechaHodl', 'GundamWhale', 'EvaPilotMint', 'SpiritGunHodl',
  'DragonSlayerWhale', 'FairyTailMint', 'CelestialHodl', 'ShinigamiWhale',
  'DeathNoteMint', 'StandUserHodl', 'OraOraWhale', 'MudaMudaMint',
  'HamonHodl', 'NenMasterWhale', 'HunterMint', 'GreedIslandHodl',
  'ChimeraWhale', 'FullCowlMint', 'OneForAllHodl', 'PlusUltraWhale',
  'DekuMint', 'ShotoHodl', 'BakugoWhale', 'GojoMint', 'SukunaHodl',
  'CursedEnergyWhale', 'BlackFlashMint', 'NezukoHodl', 'TanjiroWhale',
  'WaterBreathingMint', 'ThunderclapHodl', 'FlameHashiraWhale', 'YareYareMint',
  'StarPlatinumHodl', 'ZaWarudoWhale', 'GetsugaMint', 'HollowHodl',
  'VizardWhale', 'SoulReaperMint', 'ShonenHodl', 'IsekaiWhale', 'SenseiMint',
  'TsundereHodl', 'YanderaWhale', 'ChibiMint', 'NakamaHodl', 'MangekyoWhale',
  'IzanagiMint', 'ShadowCloneHodl', 'SageModeWhale', 'CurseMarkMint',
  'SoulSocietyHodl', 'GrandLineWhale', 'HakiMint', 'ConquerorsHodl',
  'YonkoWhale', 'WanoMint', 'GearFifthHodl', 'SaiyanPrideWhale',
  'FusionDanceMint', 'SenzuBeanHodl', 'NimbusWhale', 'SpiritBombMint',
  'FinalFlashHodl', 'MajinWhale', 'OtakuAlpha', 'AnimeOG',
] as const;

/** Simple deterministic 32-bit FNV-1a hash — stable across sessions/devices. */
function fnv1a(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

/** Deterministic collector badge name for a wallet address. */
export function holderBadgeName(address: string): string {
  return HOLDER_NAMES[fnv1a(address.toLowerCase()) % HOLDER_NAMES.length];
}

export const HOLDER_BADGE_TIP =
  'Collector badge — a fun label derived from this wallet address.';
