package svggen

// ─── Blade — The Magicless Swordsman ──────────────────────────────────────────
//
// Animated elements:
//   1. Sword light-trail arc with glowing afterimage
//   2. Motion/speed lines flashing during the strike
//   3. Sparks flying from the blade impact point
//   4. Figure with subtle combat-ready breathing
//   5. Tower spire silhouette with pulsing glow
//   6. Wind/flourish effects around the figure
//   7. Sky-rending impact energy burst

import "fmt"

func init() {
	Register(Character{
		Slug:        "blade",
		Name:        "Blade — The Magicless Swordsman",
		Description: "A warrior who defies magic itself with pure steel resolve. Every swing rends the sky and shatters enchanted barriers.",
		Attributes: []Attribute{
			{TraitType: "Background", Value: "Endless Spire"},
			{TraitType: "Signature Technique", Value: "Sky-Rending Strike"},
			{TraitType: "Element", Value: "Pure Steel / Light"},
			{TraitType: "Faction", Value: "The Unbound Order"},
			{TraitType: "Series", Value: "Original"},
			{TraitType: "Rarity", Value: "Mythic"},
		},
		Generate: generateBlade,
	})
}

func generateBlade() string {
	css := `<style>
@keyframes swordArc {
  0%   { transform: rotate(-30deg); opacity: 0; }
  10%  { opacity: 1; }
  50%  { transform: rotate(120deg); opacity: 1; }
  60%  { opacity: 0.8; }
  100% { transform: rotate(180deg); opacity: 0; }
}
@keyframes trailFade {
  0%   { opacity: 0; stroke-width: 8; }
  20%  { opacity: 0.9; stroke-width: 6; }
  50%  { opacity: 0.3; stroke-width: 3; }
  100% { opacity: 0; stroke-width: 1; }
}
@keyframes motionLine {
  0%   { opacity: 0; transform: translateX(-30px); }
  30%  { opacity: 0.7; }
  70%  { opacity: 0.3; }
  100% { opacity: 0; transform: translateX(30px); }
}
@keyframes sparkBurst {
  0%   { transform: translate(0,0) scale(1); opacity: 1; }
  100% { transform: translate(var(--dx), var(--dy)) scale(0); opacity: 0; }
}
@keyframes towerPulse {
  0%, 100% { opacity: 0.15; }
  50%      { opacity: 0.35; }
}
@keyframes breathing {
  0%, 100% { transform: translateY(0); }
  50%      { transform: translateY(-3px); }
}
@keyframes impactFlash {
  0%   { r: 5; opacity: 1; }
  50%  { r: 30; opacity: 0.3; }
  100% { r: 5; opacity: 0.8; }
}
@keyframes windSweep {
  0%   { transform: translateX(-100px); opacity: 0; }
  30%  { opacity: 0.4; }
  70%  { opacity: 0.2; }
  100% { transform: translateX(100px); opacity: 0; }
}
@keyframes textShimmer {
  0%, 100% { opacity: 0.5; }
  50%      { opacity: 1; }
}
</style>`

	body := fmt.Sprintf(`<rect width="1000" height="1000" fill="url(#bgGrad)"/>

  <!-- Sky gradient overlay -->
  <rect x="0" y="0" width="1000" height="600" fill="url(#skyGrad)" opacity="0.5"/>

  <!-- Distant tower spires -->
  <g>
    <!-- Main tower -->
    <path d="M700,600 L720,300 L730,280 L740,300 L760,600 Z" fill="url(#towerGrad)" opacity="0">
      <animate attributeName="opacity" values="0.15;0.3;0.15" dur="4s" repeatCount="indefinite"/>
    </path>
    <!-- Left tower -->
    <path d="M620,600 L635,400 L642,385 L650,400 L665,600 Z" fill="url(#towerGrad)" opacity="0">
      <animate attributeName="opacity" values="0.1;0.25;0.1" dur="5s" begin="1s" repeatCount="indefinite"/>
    </path>
    <!-- Right tower -->
    <path d="M780,600 L790,450 L797,435 L805,450 L815,600 Z" fill="url(#towerGrad)" opacity="0">
      <animate attributeName="opacity" values="0.1;0.2;0.1" dur="4.5s" begin="2s" repeatCount="indefinite"/>
    </path>
    <!-- Tower glow at top -->
    <circle cx="730" cy="280" r="8" fill="#38bdf8" filter="url(#bladeGlow)" opacity="0">
      <animate attributeName="opacity" values="0.2;0.5;0.2" dur="3s" repeatCount="indefinite"/>
    </circle>
  </g>

  <!-- Floating clouds -->
  <g opacity="0.15">
    <ellipse cx="200" cy="200" rx="150" ry="30" fill="rgba(56,189,248,0.1)" filter="url(#cloudBlur)">
      <animate attributeName="cx" values="200;250;200" dur="20s" repeatCount="indefinite"/>
    </ellipse>
    <ellipse cx="800" cy="300" rx="120" ry="25" fill="rgba(56,189,248,0.08)" filter="url(#cloudBlur)">
      <animate attributeName="cx" values="800;750;800" dur="25s" repeatCount="indefinite"/>
    </ellipse>
  </g>

  <!-- Wind sweep lines (background) -->
  <g>
    <line x1="100" y1="350" x2="300" y2="350" stroke="rgba(56,189,248,0.3)" stroke-width="1" opacity="0" style="animation: windSweep 3s ease-in-out infinite;"/>
    <line x1="150" y1="400" x2="320" y2="400" stroke="rgba(56,189,248,0.2)" stroke-width="0.8" opacity="0" style="animation: windSweep 3.5s ease-in-out infinite 1s;"/>
    <line x1="80" y1="500" x2="280" y2="500" stroke="rgba(56,189,248,0.2)" stroke-width="0.8" opacity="0" style="animation: windSweep 4s ease-in-out infinite 2s;"/>
  </g>

  <!-- Motion lines (during strike) -->
  <g>
    <rect x="300" y="420" width="400" height="2" rx="1" fill="rgba(56,189,248,0.4)" opacity="0" style="animation: motionLine 2s ease-in-out infinite;"/>
    <rect x="320" y="460" width="360" height="1.5" rx="0.75" fill="rgba(99,102,241,0.3)" opacity="0" style="animation: motionLine 2.5s ease-in-out infinite 0.5s;"/>
    <rect x="280" y="490" width="440" height="1" rx="0.5" fill="rgba(56,189,248,0.3)" opacity="0" style="animation: motionLine 2.2s ease-in-out infinite 1s;"/>
    <rect x="350" y="440" width="300" height="1.5" rx="0.75" fill="rgba(129,140,248,0.3)" opacity="0" style="animation: motionLine 2.8s ease-in-out infinite 1.5s;"/>
  </g>

  <!-- Impact flash point -->
  <circle cx="250" cy="370" fill="#38bdf8" filter="url(#bladeGlow)" style="animation: impactFlash 1.5s ease-in-out infinite;"/>

  <!-- Sword arc trail (animated path) -->
  <g transform="translate(250,370)" opacity="0">
    <path d="M0,0 Q60,-80 160,-40 Q200,-20 220,30" fill="none" stroke="url(#arcGrad)" stroke-width="4" stroke-linecap="round" filter="url(#bladeGlow)" style="animation: trailFade 2s ease-in-out infinite;"/>
    <!-- Secondary arc -->
    <path d="M0,0 Q40,-60 130,-30 Q170,-15 190,20" fill="none" stroke="rgba(99,102,241,0.4)" stroke-width="2" stroke-linecap="round" style="animation: trailFade 2s ease-in-out infinite 0.3s;"/>
  </g>

  <!-- Sword slash arc (broad stroke) -->
  <g transform="translate(250,370)">
    <path d="M0,0 Q-20,-40 -40,-90 Q-50,-120 -30,-150 Q-10,-180 20,-190 Q50,-200 80,-180 Q110,-160 120,-130" fill="none" stroke="url(#slashGrad)" stroke-width="6" stroke-linecap="round" filter="url(#bladeGlow)" opacity="0">
      <animate attributeName="opacity" values="0;0.9;0.4;0" dur="2s" repeatCount="indefinite"/>
      <animate attributeName="stroke-dashoffset" from="800" to="0" dur="2s" repeatCount="indefinite"/>
    </path>
  </g>

  <!-- Sparks from impact -->
  <g transform="translate(250,370)">
    %s
  </g>

  <!-- Blade figure -->
  <g transform="translate(460,630)" style="animation: breathing 3s ease-in-out infinite;">
    <!-- Figure shadow -->
    <ellipse cx="0" cy="100" rx="70" ry="10" fill="rgba(0,0,0,0.3)"/>

    <!-- Body / torso -->
    <path d="M-35,10 C-45,-60 -25,-140 0,-170 C25,-140 45,-60 35,10 Z" fill="url(#figureGrad)"/>

    <!-- Legs -->
    <path d="M-15,10 L-25,90 L-20,95 L-10,90 L0,15" fill="#0a0a1a"/>
    <path d="M15,10 L25,90 L20,95 L10,90 L0,15" fill="#0a0a1a"/>

    <!-- Head -->
    <ellipse cx="0" cy="-180" rx="28" ry="32" fill="#0a0a1a"/>

    <!-- Eyes - determined -->
    <ellipse cx="-10" cy="-180" rx="5" ry="3" fill="#38bdf8" filter="url(#bladeGlow)"/>
    <ellipse cx="10" cy="-180" rx="5" ry="3" fill="#38bdf8" filter="url(#bladeGlow)"/>
    <ellipse cx="-10" cy="-180" rx="2" ry="1.5" fill="#fff"/>
    <ellipse cx="10" cy="-180" rx="2" ry="1.5" fill="#fff"/>

    <!-- Hair swept back -->
    <path d="M-25,-195 C-20,-220 0,-230 20,-220 C30,-215 35,-200 28,-190" fill="#0a0a0a"/>

    <!-- Shoulder armor -->
    <path d="M-35,-140 L-50,-135 L-55,-115 L-40,-120 Z" fill="#1a1a2e" stroke="rgba(56,189,248,0.2)" stroke-width="0.8"/>
    <path d="M35,-140 L50,-135 L55,-115 L40,-120 Z" fill="#1a1a2e" stroke="rgba(56,189,248,0.2)" stroke-width="0.8"/>

    <!-- Chest armor -->
    <path d="M-18,-120 L-12,-60 L12,-60 L18,-120 Z" fill="#1a1a2e" stroke="rgba(56,189,248,0.15)" stroke-width="1"/>

    <!-- Right arm (sword arm - extended forward) -->
    <path d="M30,-130 C60,-120 80,-100 100,-80" stroke="#0a0a1a" stroke-width="14" fill="none" stroke-linecap="round"/>

    <!-- The sword -->
    <g transform="translate(100,-80) rotate(-30)">
      <!-- Blade -->
      <path d="M0,0 L5,-180 L3,-200 L0,-210 L-3,-200 L-5,-180 L0,0 Z" fill="url(#swordGrad)" filter="url(#bladeGlow)"/>
      <!-- Edge highlight -->
      <line x1="0" y1="0" x2="0" y2="-200" stroke="rgba(255,255,255,0.4)" stroke-width="0.8"/>
      <!-- Guard -->
      <path d="M-20,-5 L20,-5 L22,0 L-22,0 Z" fill="#1a1a2e" stroke="rgba(56,189,248,0.3)" stroke-width="0.5"/>
      <!-- Grip -->
      <rect x="-4" y="0" width="8" height="30" rx="2" fill="#2a1a0a"/>
      <!-- Pommel -->
      <circle cx="0" cy="32" r="4" fill="#38bdf8" filter="url(#bladeGlow)">
        <animate attributeName="opacity" values="0.4;1;0.4" dur="1.5s" repeatCount="indefinite"/>
      </circle>
    </g>

    <!-- Left arm (extended back for balance) -->
    <path d="M-30,-130 C-60,-110 -90,-90 -110,-70" stroke="#0a0a1a" stroke-width="14" fill="none" stroke-linecap="round"/>

    <!-- Cloak/cape flowing back -->
    <path d="M-25,-120 C-50,-80 -80,-40 -90,20 C-85,25 -60,10 -35,-20 C-20,-40 -15,-80 -20,-120 Z" fill="url(#capeGrad)" opacity="0.6">
      <animate attributeName="opacity" values="0.4;0.7;0.4" dur="3s" repeatCount="indefinite"/>
    </path>
    <path d="M25,-120 C50,-80 80,-40 90,20 C85,25 60,10 35,-20 C20,-40 15,-80 20,-120 Z" fill="url(#capeGrad)" opacity="0.4">
      <animate attributeName="opacity" values="0.3;0.5;0.3" dur="3.5s" repeatCount="indefinite" begin="0.5s"/>
    </path>
  </g>

  <!-- Ground / floor -->
  <path d="M0,720 L1000,720 L1000,1000 L0,1000 Z" fill="url(#groundGrad)"/>
  <!-- Ground reflection -->
  <ellipse cx="500" cy="740" rx="350" ry="15" fill="rgba(56,189,248,0.08)" filter="url(#cloudBlur)">
    <animate attributeName="opacity" values="0.05;0.12;0.05" dur="3s" repeatCount="indefinite"/>
  </ellipse>

  <!-- Ground crack lines -->
  <g opacity="0.3">
    <path d="M400,720 L380,750 L410,780 L390,810" stroke="rgba(56,189,248,0.2)" stroke-width="1.5" fill="none"/>
    <path d="M600,720 L620,745 L590,770 L615,800" stroke="rgba(99,102,241,0.15)" stroke-width="1" fill="none"/>
    <path d="M450,720 L440,740 L460,755" stroke="rgba(56,189,248,0.15)" stroke-width="1" fill="none"/>
  </g>

  <!-- Floating spark particles -->
  <g>
    %s
  </g>

  <!-- Bottom mist overlay -->
  <rect x="0" y="850" width="1000" height="150" fill="url(#bottomMist)" opacity="0">
    <animate attributeName="opacity" values="0.1;0.25;0.1" dur="5s" repeatCount="indefinite"/>
  </rect>

  <!-- Character name -->
  <g transform="translate(500,945)">
    <text text-anchor="middle" fill="rgba(56,189,248,0.6)" font-family="Georgia, serif" font-size="22" font-style="italic" letter-spacing="6" style="animation: textShimmer 3s ease-in-out infinite;">
      ✦ The Magicless Swordsman ✦
    </text>
  </g>`,

		// Generate sparks
		func() string {
			var out string
			sparks := []struct{ dx, dy, dur, delay float64 }{
				{30, -40, 1.5, 0}, {-25, -35, 1.8, 0.2}, {40, -20, 1.3, 0.4},
				{-35, -50, 2, 0.6}, {20, -60, 1.6, 0.8}, {-15, -45, 1.9, 0.3},
				{45, -30, 1.4, 0.7}, {-40, -25, 1.7, 0.5},
			}
			for _, s := range sparks {
				out += fmt.Sprintf(`
    <circle cx="0" cy="0" r="2.5" fill="#38bdf8" filter="url(#bladeGlow)" opacity="0">
      <animate attributeName="cx" values="0;%.1f;%.1f" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
      <animate attributeName="cy" values="0;%.1f;%.1f" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0;1;0" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
    </circle>`, s.dx*0.5, s.dx, s.dur, s.delay, s.dy*0.5, s.dy, s.dur, s.delay, s.dur, s.delay)
			}
			return out
		}(),

		// Generate floating particles
		func() string {
			var out string
			particles := []struct{ x, y, r, dur, delay float64 }{
				{200, 500, 2, 4, 0}, {800, 450, 1.5, 5, 0.5}, {300, 300, 2.5, 6, 1},
				{700, 600, 2, 4.5, 1.5}, {150, 650, 1.5, 5.5, 0.3}, {850, 350, 2, 3.5, 2},
				{350, 200, 1.5, 5, 2.5}, {650, 250, 2, 4, 0.8},
			}
			for _, p := range particles {
				out += fmt.Sprintf(`
    <circle cx="%.1f" cy="%.1f" r="%.1f" fill="#38bdf8" opacity="0">
      <animate attributeName="cy" values="%.1f;%.1f" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
      <animate attributeName="opacity" values="0;0.6;0" dur="%.1fs" begin="%.1fs" repeatCount="indefinite"/>
    </circle>`, p.x, p.y, p.r, p.y, p.y-250, p.dur, p.delay, p.dur, p.delay)
			}
			return out
		}(),
	)

	svg := SVG(css, body)
	svg = svg[:len(svg)-6] +
		"\n" + RadialGrad("bgGrad", 0.5, 0.4, 0.9,
			Stop("0%", "#001a2e", 1)+
				Stop("30%", "#001a33", 1)+
				Stop("60%", "#000d1a", 1)+
				Stop("100%", "#00050a", 1)) +
		"\n" + LinearGrad("skyGrad", 0, 0, 0, 1,
			Stop("0%", "rgba(56,189,248,0.15)", 1)+
				Stop("60%", "rgba(56,189,248,0.05)", 1)+
				Stop("100%", "rgba(56,189,248,0)", 1)) +
		"\n" + LinearGrad("arcGrad", 0, 0, 1, 1,
			Stop("0%", "#38bdf8", 1)+
				Stop("50%", "#818cf8", 1)+
				Stop("100%", "rgba(56,189,248,0)", 1)) +
		"\n" + LinearGrad("slashGrad", 0, 1, 1, 0,
			Stop("0%", "#38bdf8", 1)+
				Stop("50%", "#e0f2fe", 1)+
				Stop("100%", "#818cf8", 1)) +
		"\n" + RadialGrad("figureGrad", 0.5, 0.4, 0.8,
			Stop("0%", "#0a0a2e", 1)+
				Stop("50%", "#0a0a1a", 1)+
				Stop("100%", "#00000a", 1)) +
		"\n" + LinearGrad("capeGrad", 0, 0, 1, 1,
			Stop("0%", "#0a0a2e", 1)+
				Stop("100%", "#00001a", 1)) +
		"\n" + RadialGrad("towerGrad", 0.5, 0.3, 0.7,
			Stop("0%", "#0a1a2e", 1)+
				Stop("100%", "#000a15", 1)) +
		"\n" + LinearGrad("swordGrad", 0, 0, 0, 1,
			Stop("0%", "#e0f2fe", 1)+
				Stop("30%", "#bae6fd", 1)+
				Stop("100%", "#38bdf8", 1)) +
		"\n" + RadialGrad("groundGrad", 0.5, 0.7, 0.8,
			Stop("0%", "#0a1a2e", 1)+
				Stop("60%", "#050d1a", 1)+
				Stop("100%", "#000005", 1)) +
		"\n" + LinearGrad("bottomMist", 0, 0, 0, 1,
			Stop("0%", "rgba(56,189,248,0)", 1)+
				Stop("100%", "rgba(56,189,248,0.15)", 1)) +
		"\n" + GlowFilter("bladeGlow", "#38bdf8", 4) +
		`<filter id="cloudBlur" x="-50%" y="-50%" width="200%" height="200%">
  <feGaussianBlur in="SourceGraphic" stdDeviation="20"/>
</filter>` +
		"\n</svg>"
	return svg
}
