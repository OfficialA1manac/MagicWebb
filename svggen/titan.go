package svggen

// ─── Titan — The Cosmic Awakening ─────────────────────────────────────────────
//
// Animated elements:
//   1. Core energy orb pulsing with cosmic power
//   2. Energy cracks radiating from center (glowing lines)
//   3. Power rings expanding and rotating
//   4. Cosmic particles orbiting and floating
//   5. Shattered ground pieces with floating animation
//   6. Nebula background with slow color shift
//   7. Golden energy veins pulsing through the figure

import "fmt"

func init() {
	Register(Character{
		Slug:        "titan",
		Name:        "Titan — The Cosmic Awakening",
		Description: "A warrior breaking through cosmic restraints, channeling the raw energy of collapsing stars. The awakening shakes reality itself.",
		Attributes: []Attribute{
			{TraitType: "Background", Value: "Cosmic Nebula"},
			{TraitType: "Signature Technique", Value: "Void-Shattering Strike"},
			{TraitType: "Element", Value: "Cosmic / Golden Energy"},
			{TraitType: "Faction", Value: "The Wild Hunt"},
			{TraitType: "Series", Value: "Original"},
			{TraitType: "Rarity", Value: "Mythic"},
		},
		Generate: generateTitan,
	})
}

func generateTitan() string {
	css := `<style>
@keyframes corePulse {
  0%, 100% { r: 45; opacity: 0.9; }
  50%      { r: 55; opacity: 1; }
}
@keyframes coreGlowPulse {
  0%, 100% { r: 70; opacity: 0.4; }
  50%      { r: 90; opacity: 0.7; }
}
@keyframes energyCrack {
  0%, 100% { opacity: 0.6; }
  50%      { opacity: 1; }
}
@keyframes ringExpand {
  0%   { r: 30; opacity: 0.8; stroke-width: 4; }
  100% { r: 250; opacity: 0; stroke-width: 1; }
}
@keyframes ringSpin {
  0%   { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}
@keyframes particleOrbit {
  0%   { transform: rotate(0deg) translateX(-180px) rotate(0deg); }
  100% { transform: rotate(360deg) translateX(-180px) rotate(-360deg); }
}
@keyframes particleOrbit2 {
  0%   { transform: rotate(60deg) translateX(-220px) rotate(-60deg); }
  100% { transform: rotate(420deg) translateX(-220px) rotate(-420deg); }
}
@keyframes floatDebris {
  0%   { transform: translateY(0) rotate(0deg); opacity: 0.6; }
  50%  { transform: translateY(-30px) rotate(180deg); opacity: 0.3; }
  100% { transform: translateY(0) rotate(360deg); opacity: 0.6; }
}
@keyframes nebulaShift {
  0%, 100% { opacity: 0.3; }
  50%      { opacity: 0.6; }
}
@keyframes textPulse {
  0%, 100% { opacity: 0.5; }
  50%      { opacity: 1; }
}
</style>`

	body := fmt.Sprintf(`<rect width="1000" height="1000" fill="url(#bgGrad)"/>

  <!-- Nebula clouds layer 1 -->
  <g>
    <ellipse cx="300" cy="400" rx="400" ry="250" fill="url(#nebula1)" filter="url(#nebulaBlur)" opacity="0">
      <animate attributeName="opacity" values="0.3;0.5;0.3" dur="12s" repeatCount="indefinite"/>
    </ellipse>
  </g>
  <!-- Nebula clouds layer 2 -->
  <g>
    <ellipse cx="700" cy="600" rx="350" ry="200" fill="url(#nebula2)" filter="url(#nebulaBlur)" opacity="0">
      <animate attributeName="opacity" values="0.2;0.4;0.2" dur="15s" repeatCount="indefinite" begin="3s"/>
    </ellipse>
  </g>

  <!-- Stars background -->
  <g opacity="0.4">
    <circle cx="100" cy="100" r="1.5" fill="#fff"><animate attributeName="opacity" values="0.3;1;0.3" dur="2s" repeatCount="indefinite"/></circle>
    <circle cx="900" cy="80" r="2" fill="#fff"><animate attributeName="opacity" values="0.5;1;0.5" dur="3s" repeatCount="indefinite"/></circle>
    <circle cx="150" cy="500" r="1" fill="#fff"><animate attributeName="opacity" values="0.2;0.8;0.2" dur="4s" repeatCount="indefinite"/></circle>
    <circle cx="850" cy="350" r="1.5" fill="#fff"><animate attributeName="opacity" values="0.4;0.9;0.4" dur="2.5s" repeatCount="indefinite"/></circle>
    <circle cx="200" cy="800" r="1" fill="#fff"><animate attributeName="opacity" values="0.3;0.7;0.3" dur="3.5s" repeatCount="indefinite"/></circle>
    <circle cx="800" cy="850" r="1.2" fill="#fff"><animate attributeName="opacity" values="0.5;1;0.5" dur="2.8s" repeatCount="indefinite"/></circle>
  </g>

  <!-- Central energy core glow -->
  <circle cx="500" cy="450" fill="url(#coreGlowGrad)" opacity="0">
    <animate attributeName="r" values="70;90;70" dur="3s" repeatCount="indefinite"/>
    <animate attributeName="opacity" values="0.3;0.6;0.3" dur="3s" repeatCount="indefinite"/>
  </circle>

  <!-- Energy core -->
  <circle cx="500" cy="450" fill="url(#coreGrad)" opacity="0">
    <animate attributeName="r" values="45;55;45" dur="3s" repeatCount="indefinite"/>
    <animate attributeName="opacity" values="0.9;1;0.9" dur="3s" repeatCount="indefinite"/>
  </circle>
  <circle cx="500" cy="450" r="20" fill="rgba(255,255,200,0.4)" filter="url(#goldGlow)">
    <animate attributeName="r" values="18;25;18" dur="2s" repeatCount="indefinite"/>
  </circle>

  <!-- Expanding power rings -->
  <g transform="translate(500,450)">
    <circle cx="0" cy="0" fill="none" stroke="rgba(168,85,247,0.6)" stroke-width="3">
      <animate attributeName="r" values="30;250" dur="4s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.8;0" dur="4s" repeatCount="indefinite"/>
    </circle>
    <circle cx="0" cy="0" fill="none" stroke="rgba(168,85,247,0.4)" stroke-width="2">
      <animate attributeName="r" values="30;250" dur="4s" begin="1s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.6;0" dur="4s" begin="1s" repeatCount="indefinite"/>
    </circle>
    <circle cx="0" cy="0" fill="none" stroke="rgba(56,189,248,0.4)" stroke-width="2">
      <animate attributeName="r" values="30;250" dur="4s" begin="2s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.6;0" dur="4s" begin="2s" repeatCount="indefinite"/>
    </circle>
    <circle cx="0" cy="0" fill="none" stroke="rgba(251,191,36,0.3)" stroke-width="1.5">
      <animate attributeName="r" values="30;250" dur="4s" begin="3s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.5;0" dur="4s" begin="3s" repeatCount="indefinite"/>
    </circle>
  </g>

  <!-- Orbiting rings (rotating) -->
  <g transform="translate(500,450)">
    <ellipse cx="0" cy="0" rx="160" ry="50" fill="none" stroke="rgba(168,85,247,0.3)" stroke-width="2" transform="rotate(30)">
      <animateTransform attributeName="transform" type="rotate" from="30" to="390" dur="12s" repeatCount="indefinite"/>
    </ellipse>
    <ellipse cx="0" cy="0" rx="200" ry="60" fill="none" stroke="rgba(56,189,248,0.2)" stroke-width="1.5" transform="rotate(-20)">
      <animateTransform attributeName="transform" type="rotate" from="-20" to="340" dur="15s" repeatCount="indefinite"/>
    </ellipse>
  </g>

  <!-- Orbiting particles -->
  <g transform="translate(500,450)">
    <g style="animation: particleOrbit 8s linear infinite;">
      <circle cx="0" cy="0" r="4" fill="#a78bfa" filter="url(#purpleGlow)"/>
    </g>
    <g style="animation: particleOrbit2 10s linear infinite;">
      <circle cx="0" cy="0" r="3" fill="#38bdf8" filter="url(#blueGlow)"/>
    </g>
  </g>

  <!-- Energy cracks radiating from center -->
  <g transform="translate(500,450)">
    <path d="M0,0 L-80,-150 L-120,-130 L-180,-200" stroke="url(#crackGrad)" stroke-width="3" fill="none" stroke-linecap="round" filter="url(#goldGlow)">
      <animate attributeName="opacity" values="0.5;1;0.5" dur="2s" repeatCount="indefinite"/>
    </path>
    <path d="M0,0 L100,-120 L140,-100 L200,-160" stroke="url(#crackGrad)" stroke-width="2.5" fill="none" stroke-linecap="round">
      <animate attributeName="opacity" values="0.4;0.9;0.4" dur="2.5s" repeatCount="indefinite" begin="0.5s"/>
    </path>
    <path d="M0,0 L-60,140 L-100,120 L-160,200" stroke="url(#crackGrad)" stroke-width="2" fill="none" stroke-linecap="round">
      <animate attributeName="opacity" values="0.3;0.8;0.3" dur="3s" repeatCount="indefinite" begin="1s"/>
    </path>
    <path d="M0,0 L80,130 L120,110 L170,180" stroke="url(#crackGrad)" stroke-width="2.5" fill="none" stroke-linecap="round">
      <animate attributeName="opacity" values="0.5;1;0.5" dur="2.2s" repeatCount="indefinite" begin="0.3s"/>
    </path>
    <path d="M0,0 L-180,-50 L-220,-30" stroke="url(#crackGrad)" stroke-width="2" fill="none" stroke-linecap="round">
      <animate attributeName="opacity" values="0.3;0.7;0.3" dur="3.5s" repeatCount="indefinite" begin="1.5s"/>
    </path>
    <path d="M0,0 L180,40 L220,20" stroke="url(#crackGrad)" stroke-width="2" fill="none" stroke-linecap="round">
      <animate attributeName="opacity" values="0.4;0.8;0.4" dur="2.8s" repeatCount="indefinite" begin="0.8s"/>
    </path>
    <path d="M0,0 L-200,80 L-240,70" stroke="url(#crackGrad)" stroke-width="1.5" fill="none" stroke-linecap="round">
      <animate attributeName="opacity" values="0.2;0.6;0.2" dur="4s" repeatCount="indefinite" begin="2s"/>
    </path>
  </g>

  <!-- Titan figure silhouette -->
  <g transform="translate(500,680)">
    <!-- Body / torso -->
    <path d="M-60,0 C-70,-80 -50,-180 -20,-220 C20,-260 60,-240 80,-200 C100,-160 90,-80 70,0 Z" fill="url(#titanGrad)"/>
    <!-- Head -->
    <ellipse cx="10" cy="-240" rx="40" ry="45" fill="#0a0a0a"/>
    <!-- Jaw line -->
    <path d="M-25,-220 C-15,-200 35,-200 45,-220" fill="none" stroke="#1a1a1a" stroke-width="3"/>
    <!-- Eyes - glowing gold -->
    <ellipse cx="-5" cy="-240" rx="10" ry="5" fill="#fbbf24" filter="url(#goldGlow)"/>
    <ellipse cx="25" cy="-240" rx="10" ry="5" fill="#fbbf24" filter="url(#goldGlow)"/>
    <ellipse cx="-5" cy="-240" rx="4" ry="2" fill="#fff"/>
    <ellipse cx="25" cy="-240" rx="4" ry="2" fill="#fff"/>

    <!-- Chest / armor plate -->
    <path d="M-30,-160 L-20,-80 L50,-80 L40,-160 Z" fill="#1a1a1a" stroke="rgba(168,85,247,0.3)" stroke-width="1.5"/>

    <!-- Golden energy veins on chest -->
    <path d="M0,-150 L5,-120 L10,-100" stroke="#fbbf24" stroke-width="1.5" fill="none" opacity="0.6">
      <animate attributeName="opacity" values="0.3;0.8;0.3" dur="2s" repeatCount="indefinite"/>
    </path>
    <path d="M20,-140 L15,-110 L12,-90" stroke="#fbbf24" stroke-width="1.5" fill="none" opacity="0.6">
      <animate attributeName="opacity" values="0.4;0.9;0.4" dur="2.5s" repeatCount="indefinite" begin="0.5s"/>
    </path>

    <!-- Arms extended -->
    <path d="M-60,0 C-100,-40 -130,-80 -150,-120" stroke="#0a0a0a" stroke-width="25" fill="none" stroke-linecap="round"/>
    <path d="M70,0 C110,-40 140,-80 160,-120" stroke="#0a0a0a" stroke-width="25" fill="none" stroke-linecap="round"/>

    <!-- Hand energy bursts -->
    <circle cx="-150" cy="-120" r="15" fill="rgba(168,85,247,0.4)" filter="url(#purpleGlow)">
      <animate attributeName="r" values="12;18;12" dur="1.5s" repeatCount="indefinite"/>
    </circle>
    <circle cx="160" cy="-120" r="15" fill="rgba(56,189,248,0.4)" filter="url(#blueGlow)">
      <animate attributeName="r" values="12;18;12" dur="1.5s" begin="0.5s" repeatCount="indefinite"/>
    </circle>
  </g>

  <!-- Floating shattered ground debris -->
  <g transform="translate(500,850)">
    <path d="M-100,20 L-80,-10 L-60,0 L-70,30 Z" fill="#1a1a1a" opacity="0">
      <animateTransform attributeName="transform" type="translate" values="0,0;-20,-40;0,0" dur="6s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.4;0.7;0.4" dur="6s" repeatCount="indefinite"/>
    </path>
    <path d="M60,40 L80,10 L110,20 L90,50 Z" fill="#2a2a2a" opacity="0">
      <animateTransform attributeName="transform" type="translate" values="0,0;25,-30;0,0" dur="7s" begin="1s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.3;0.6;0.3" dur="7s" begin="1s" repeatCount="indefinite"/>
    </path>
    <path d="M-40,50 L-20,30 L0,45 L-10,65 Z" fill="#151515" opacity="0">
      <animateTransform attributeName="transform" type="translate" values="0,0;-15,-50;0,0" dur="8s" begin="2s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.3;0.5;0.3" dur="8s" begin="2s" repeatCount="indefinite"/>
    </path>
    <path d="M30,50 L50,20 L70,35 L55,60 Z" fill="#1a1a1a" opacity="0">
      <animateTransform attributeName="transform" type="translate" values="0,0;15,-45;0,0" dur="6.5s" begin="3s" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0.2;0.5;0.2" dur="6.5s" begin="3s" repeatCount="indefinite"/>
    </path>
  </g>

  <!-- Ground crack lines -->
  <g transform="translate(500,800)" opacity="0.4">
    <path d="M0,0 L-50,20 L-80,15 L-120,40" stroke="rgba(168,85,247,0.3)" stroke-width="2" fill="none"/>
    <path d="M0,0 L40,30 L70,25 L110,50" stroke="rgba(168,85,247,0.3)" stroke-width="2" fill="none"/>
    <path d="M0,0 L-30,40 L-20,70" stroke="rgba(56,189,248,0.2)" stroke-width="1.5" fill="none"/>
    <path d="M0,0 L20,50 L15,80" stroke="rgba(56,189,248,0.2)" stroke-width="1.5" fill="none"/>
  </g>

  <!-- Floating cosmic particles -->
  <g>
    %s
  </g>

  <!-- Bottom glow reflection -->
  <ellipse cx="500" cy="850" rx="300" ry="40" fill="url(#groundGlow)" opacity="0">
    <animate attributeName="opacity" values="0.2;0.4;0.2" dur="4s" repeatCount="indefinite"/>
  </ellipse>

  <!-- Character name -->
  <g transform="translate(500,940)">
    <text text-anchor="middle" fill="rgba(168,85,247,0.6)" font-family="Georgia, serif" font-size="22" font-style="italic" letter-spacing="6" style="animation: textPulse 3s ease-in-out infinite;">
      ✦ The Cosmic Awakening ✦
    </text>
  </g>`,
		// Generate cosmic particles
		func() string {
			var out string
			type particleDef struct{ x, y, r, dur, delay float64; fill string }
			particles := []particleDef{
				{120, 600, 2, 4, 0, "rgba(251,191,36,0.7)"},
				{880, 300, 2.5, 5, 0.5, "rgba(168,85,247,0.7)"},
				{250, 200, 1.5, 3.5, 1, "rgba(56,189,248,0.6)"},
				{750, 700, 2, 6, 1.5, "rgba(251,191,36,0.7)"},
				{300, 800, 1.5, 4.5, 0.3, "rgba(168,85,247,0.7)"},
				{700, 150, 3, 5.5, 2, "rgba(56,189,248,0.6)"},
				{180, 400, 2, 3, 2.5, "rgba(251,191,36,0.7)"},
				{820, 550, 1.5, 4, 0.8, "rgba(168,85,247,0.7)"},
				{400, 100, 2, 5, 1.2, "rgba(56,189,248,0.6)"},
				{600, 850, 2.5, 4.5, 3, "rgba(251,191,36,0.7)"},
				{350, 650, 1, 3.5, 0.2, "rgba(168,85,247,0.7)"},
				{650, 500, 2, 4, 1.8, "rgba(56,189,248,0.6)"},
			}
			for _, p := range particles {
				out += fmt.Sprintf(`
    <circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s" opacity="0">
      <animate attributeName="cy" values="%.1f;%.1f" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0;0.8;0" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
    </circle>`, p.x, p.y, p.r, p.fill, p.y, p.y-200, p.dur, p.delay, p.dur, p.delay)
			}
			return out
		}(),
	)

	svg := SVG(css, body)
	svg = svg[:len(svg)-6] +
		"\n" + RadialGrad("bgGrad", 0.5, 0.5, 0.9,
			Stop("0%", "#05001a", 1)+
				Stop("30%", "#0a002a", 1)+
				Stop("60%", "#150030", 1)+
				Stop("100%", "#000005", 1)) +
		"\n" + RadialGrad("nebula1", 0.3, 0.4, 0.6,
			Stop("0%", "rgba(88,28,135,0.4)", 1)+
				Stop("100%", "rgba(88,28,135,0)", 1)) +
		"\n" + RadialGrad("nebula2", 0.7, 0.6, 0.5,
			Stop("0%", "rgba(15,118,110,0.3)", 1)+
				Stop("100%", "rgba(15,118,110,0)", 1)) +
		"\n" + RadialGrad("coreGrad", 0.5, 0.45, 0.5,
			Stop("0%", "#fff8e0", 1)+
				Stop("20%", "#fbbf24", 1)+
				Stop("60%", "#a78bfa", 1)+
				Stop("100%", "rgba(88,28,135,0)", 1)) +
		"\n" + RadialGrad("coreGlowGrad", 0.5, 0.45, 0.5,
			Stop("0%", "rgba(251,191,36,0.4)", 1)+
				Stop("50%", "rgba(168,85,247,0.2)", 1)+
				Stop("100%", "rgba(168,85,247,0)", 1)) +
		"\n" + LinearGrad("crackGrad", 0, 0, 1, 0,
			Stop("0%", "#fbbf24", 1)+
				Stop("50%", "#a78bfa", 1)+
				Stop("100%", "#38bdf8", 1)) +
		"\n" + RadialGrad("titanGrad", 0.5, 0.5, 0.8,
			Stop("0%", "#1a1a2e", 1)+
				Stop("60%", "#0a0a1a", 1)+
				Stop("100%", "#000000", 1)) +
		"\n" + RadialGrad("groundGlow", 0.5, 0.5, 0.5,
			Stop("0%", "rgba(168,85,247,0.3)", 1)+
				Stop("100%", "rgba(168,85,247,0)", 1)) +
		"\n" + GlowFilter("goldGlow", "#fbbf24", 5) +
		"\n" + GlowFilter("purpleGlow", "#a78bfa", 4) +
		"\n" + GlowFilter("blueGlow", "#38bdf8", 4) +
		`<filter id="nebulaBlur" x="-50%" y="-50%" width="200%" height="200%">
  <feGaussianBlur in="SourceGraphic" stdDeviation="40"/>
</filter>` +
		"\n</svg>"
	return svg
}
