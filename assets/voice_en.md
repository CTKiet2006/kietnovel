## Prose Standards

These are quality criteria, not a checklist to tick item by item. A chapter has to read naturally first; only then do you check the boxes.

- Open with conflict, mystery, appetite, or something unsettling as early as possible. Avoid abstract recap.
- Advance the plot through action, dialogue, and sensory detail. Avoid summary and overview.
- Dialogue must carry each character's voice, with subtext and an ulterior purpose. No lecturing.
- Emotion shows through bodily reaction and choice, never through a label slapped on.
- Relationship shifts need a triggering event. Nobody goes from stranger to absolute trust inside one chapter.
- Release secrets in installments. Do not pre-explain a major reveal the outline has not called for.
- The closing hook can be a crisis, a decision, an emotional aftershock, a shift in relationship, or an unfinished goal. Not every chapter needs an exaggerated cliffhanger.
- **No AI-sounding filler**: avoid every pattern listed in `reference_pack.references.anti_ai_tone` (five groups: structure / word choice / description / dialogue / rhythm). Fatigue words and canned-phrase thresholds are enumerated in `working_memory.user_rules.structured` and checked mechanically at commit.
- **Banned crutches (English)**: do not reach for empty stock phrases like "little did he know", "a chill ran down", "in that moment", "little did they realize", "sent a wave of", "it was as if", "a mix of", "the air was thick with". Use any given cliché at most once per chapter.
- **Sentence variety**: `episodic_memory.style_stats` (if present) is a tally of the prose you have already written — a mirror of your own verbal habits. Actively suppress the high-frequency items. The most common culprits are the corrective construction ("not X, but Y"), a single time-measure repeated ("moments later", "seconds later"), and a run of same-shape similes. Rotate the chapter-ending form (severed short line / trailing dialogue / lingering image / hanging question), and avoid opening every chapter with a stock time-of-day.
- **Do not recap**: summaries, foreshadowing notes, and states in `episodic_memory` are a record of what has already been written, for continuity checking, not raw material for the new chapter. Only touch a previous chapter's information when the plot calls for it, from a fresh angle. Recap-style rewrites are forbidden — verbatim repetition across chapters is recorded by `style_stats.repeated_sentences`.
