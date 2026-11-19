# Fiabilité, sécurité et simplification de Prism

Plan de travail interne — 10 novembre 2026.

## Principes

Conserver les capacités génériques que l'agent peut combiner. Corriger les
limites démontrées avant d'ajouter des outils ou des abstractions. Les parcours
doivent rester simples pour les petits modèles et pour les utilisateurs.

Chaque jalon doit fonctionner en mode personnel et en mode multi. Les droits
s'appliquent à l'exécution et au catalogue présenté à l'agent. Une conversation
partagée n'acquiert pas les droits personnels de son administrateur.

Les changements locaux de suivi des workspaces et de Room présents avant ce
chantier sont conservés ; ils ne constituent pas une livraison de ce plan.

## Jalon 1 — Sécurité et cohérence des droits

- [x] Mettre à jour Go, le parseur réseau et Excelize vers les versions corrigées.
- [x] Rattacher les livraisons Webex au groupe de la session ; refuser une
  destination manquante ou ambiguë sans choisir celle d'un autre groupe.
- [x] Vérifier les modèles autorisés du groupe lors de la configuration de Room
  et avant chaque exécution de son agent, Webex et Teams compris.
- [x] Appliquer les désactivations globales d'outils aux administrateurs, aux
  appels HTTP directs et aux conversations sans interface.
- [x] Rafraîchir les droits et scopes du chat web entre les tours, sans imposer
  une reconnexion.
- [x] Inclure Teams dans la recherche de l'historique partagé du groupe.
- [x] Ajouter à la CI le scan des appels Go vulnérables, les tests navigateur
  des pièces jointes/Teams/téléphonie et la base des tests Teams.

Critère : les régressions reproduites pendant la revue sont couvertes et passent,
les autres groupes restent exclus, les usages autorisés continuent de fonctionner.
Le scan ne doit signaler aucune vulnérabilité appelée par le code.

État : implémenté, vérifié et installé sur les instances locales mono et multi.
La nouvelle CI n'a pas encore été exécutée sur GitHub ; les changements ne sont
pas publiés sur le dépôt distant.

## Jalon 2 — Chat fiable et historique borné dans l'interface

- [x] Garder le brouillon et toutes ses pièces jointes si le WebSocket est fermé
  ou si l'envoi échoue immédiatement.
- [x] Confirmer la prise en charge du message par le serveur. Conserver un
  identifiant de demande pour distinguer un message reçu d'un envoi incertain et
  éviter les doublons lors d'une reprise.
- [x] Distinguer clairement attente de connexion, demande acceptée, exécution,
  attente utilisateur, interruption et échec ; réutiliser les états existants.
- [x] Charger les anciens échanges par pages dans le chat personnel, sans
  confondre l'archive affichée avec le contexte envoyé au modèle.
- [x] Préserver les pièces jointes, traces d'outils et réponses au fil des pages,
  du rechargement, de la reconnexion et d'un changement de workspace.

Critère : une coupure ne fait pas perdre silencieusement une demande ni lancer
deux fois la même action. Ouvrir un long historique a un coût borné ; les anciens
messages restent consultables. Valider avec faux fournisseur et navigateur,
y compris une réponse en cours pendant la reconnexion.

Le serveur enregistre le reçu et le message utilisateur dans une transaction
avant confirmation. Les reprises vérifient le reçu sans renvoi automatique ;
un renvoi manuel garde le même identifiant. Le contexte modèle reste distinct
des pages d'archive affichées. Les chargements de fichiers terminant après un
changement de workspace restent rattachés au brouillon de départ.

## Jalon 3 — Navigation et réglages plus prévisibles

- [x] Rendre le contexte courant visible : assistant général, workspace ou
  groupe. Relire notamment le passage entre une application et le chat.
- [x] Partager un groupe actif entre les rubriques de l'administration, en
  conservant sa sélection tant qu'il reste accessible.
- [x] Regrouper les réglages autour des usages : agent et modèles, connexions,
  outils et connaissances, apparence, administration. Garder les options rares
  dans des sections avancées plutôt qu'ajouter des écrans.
- [x] Harmoniser les sauvegardes et leurs retours : modification non enregistrée,
  succès, erreur et état de connexion réel.
- [x] Garder la configuration téléphonie dans Admin/Settings et le trafic,
  les journaux et la programmation des appels dans l'application Téléphonie.

Critère : changer de rubrique ne change pas implicitement le groupe administré ;
les actions du chat visent le contexte affiché. Vérifier personnel, membre,
administrateur de groupe et administrateur global, thèmes et écran étroit.

## Jalon 4 — Agent plus léger, mémoire et travail consultables

- [x] Retirer automatiquement du catalogue des agents limités et des canaux
  sans interface les capacités réellement inaccessibles. Examiner aussi les actions et paramètres d'un outil composite,
  en conservant une définition simple et des contrôles à l'exécution.
- [x] Mesurer le coût du prompt et les résultats avec le banc d'évaluation
  existant : réussite des tâches, appels d'outils, erreurs et latence. La revue a
  mesuré environ 65 ko pour instructions + schémas natifs en profil standard,
  hors personnalité, historique et outils MCP ; ce n'est pas un nombre de tokens.
- [x] Rendre consultables et corrigeables le résumé de contexte, le profil, les
  souvenirs et les compétences en réutilisant les surfaces existantes.
- [x] Étendre `search_history` avec la lecture d'une session ou d'un message
  précis (`message_id`), sans outil supplémentaire. Les messages longs se lisent
  par fragments avec `offset` / `nextOffset`, sous la limite de sortie de
  l'exécuteur ; les frontières personnelles et de groupe restent identiques.
- [x] Montrer les travaux en cours dans Activité à partir des états de tâches
  et des API de suivi déjà disponibles, avec leurs droits existants.

Critère : l'agent garde ses capacités utiles avec moins de contexte inutile ;
les tâches de référence restent réalisables sans appels de découverte imposés.
L'utilisateur voit ce que l'agent retient et où en est son travail, sans exposer
les conversations personnelles ou celles d'un autre groupe.

## Décision de catalogue

Le chat web administrateur conserve ses contrats d'outils complets. Le filtrage
initial avait entraîné davantage de vérifications dans plusieurs scénarios réels ;
le gain de taille ne suffisait pas à justifier ce changement de comportement.
Les agents avec des droits limités et les canaux sans interface gardent le
catalogue adapté. Les contrôles d'exécution et désactivations restent appliqués
pour tous les rôles. Aucun réglage ni appel de découverte n'a été ajouté.

La lecture d'archive accepte aussi un ID déjà connu par les métadonnées du
workspace ; elle n'impose pas de recherche préalable. Le message est identifié
exactement et sa lecture paginée conserve le texte complet, même au-delà de la
limite d'un résultat d'outil.

## Validation et livraison

Pour le chantier complet : suite Go avec détecteur de courses et PostgreSQL
jetable, `go vet`, tests Node et quatre parcours Chromium réussis. Les
régressions de droits et de livraison utilisent de faux services ; les essais
complémentaires du modèle sont décrits ci-dessous. Aucun message Webex ou Teams
n'a été envoyé. Le scan Go ne trouve plus d'appel vulnérable ; un avis subsiste
dans une dépendance dont la fonction concernée n'est pas utilisée par Prism.

Garder une livraison vérifiable par jalon. Adapter la documentation fonctionnelle
aux changements visibles et vérifier les traductions. Ne pas redémarrer une
instance qui exécute un travail sans traiter explicitement son interruption.
Les instances locales ont été mises à jour après vérification de l'absence de
travail en cours. Commit, push et livraison distante restent à effectuer.

### Vérifications du chantier complet

- Les reçus survivent à une coupure et à un redémarrage. Les doublons et un
  contenu modifié sous le même identifiant ne relancent pas le modèle.
- Les pages préservent les noms/arguments des lots d'outils coupés en deux.
  Les relectures longues restituent un message complet avec caractères
  multioctets, sans troncature par l'exécuteur.
- Les tests vérifient les deux résumés, leur frontière, les corrections
  concurrentes, les compétences personnelles et l'exclusion des autres scopes.
- Chromium couvre reçus/reprise, pièces jointes à cheval sur deux pages,
  brouillons par workspace, échec puis succès d'une correction, compétences,
  travaux en cours, menus avancés, affichage mobile, Teams et téléphonie.
- Le vrai modèle `flash-next`, avec l'image finale, a retrouvé et relu une archive
  de test au-delà de l'extrait de recherche en deux appels, sans erreur, puis a
  restitué le code demandé. Les deux conversations synthétiques ont été supprimées.
- Le banc d'évaluation crée désormais un workspace via l'API et utilise son ID
  retourné. Il ne supprime plus un nom avant de le réutiliser. Le contrôle d'heure
  reconnaît aussi `22h` et `22h00`, et rejette `22h30` / `22:30`. Le comptage
  accepte `Trois` en plus de `3` et `three`. Les rapports d'origine sont conservés ;
  les trois faux négatifs d'heure de la référence et l'unique faux négatif de
  comptage de l'image finale sont corrigés dans des copies identifiées, sans
  changer réponses, contrôles de fichiers ni mesures.

### Comparaison du modèle

Cinq scénarios existants exécutés trois fois chacun, avant puis après : création
de fichier, création/comptage de fichiers, calcul Python, réponse horaire et
explication sans widget. Modèle et configuration identiques (`flash-next`).

| Mesure | Référence | Image finale |
| --- | ---: | ---: |
| Tâches réussies, contrôles bilingues corrigés | 15/15 | 15/15 |
| Appels d'outils moyens | 0,733 | 0,667 |
| Erreurs d'outils moyennes | 0 | 0 |
| Durée moyenne | 9,42 s | 9,96 s |

La comparaison passe les seuils existants : aucun recul de réussite, pas plus de
15 % d'appels d'outils supplémentaires et pas plus de 0,25 erreur supplémentaire.
Cet échantillon limité et non déterministe ne démontre ni un gain de vitesse ni
une amélioration générale de qualité.

Rapports locaux : `/tmp/prism-improvements-eval-before3-verified.json` et
`/tmp/prism-improvements-eval-preserved3-verified.json`, avec référence aux
rapports bruts. L'essai d'archive est conservé dans
`/tmp/prism-history-model-eval.json`.

### Livraison locale

La première livraison locale utilisait l'image
`sha256:c2972d0fd1681e7a66300b65ee11ca9cf86496fc28c2146c02c8182e5ef4900f`,
étiquetée `blazux/prism:local-simplification-20261110`. La comparaison de modèle
ci-dessus concerne cette première livraison, avant la seconde revue des scopes. Les configurations, ports
et modèles sont conservés. Les anciennes images locales sont gardées sous les
étiquettes `local-before-simplification-mono-20261110` et
`local-before-simplification-multi-20261110` pour permettre un retour arrière.

La base PostgreSQL jetable des tests a été arrêtée. Commit et push n'ont pas été
demandés. La validation manuelle par l'utilisateur reste à faire.

## Seconde revue — réglages indépendants des workspaces

La première passe n'avait pas séparé correctement configuration, mémoire
personnelle et contexte d'une conversation. Corrections du 10 novembre :

- Les liens, l'iframe et la page Settings ne transportent plus le workspace
  ouvert. Aucun badge de session, lecture de `active-workspace` ou paramètre
  implicite ne choisit les connexions ou la mémoire affichées.
- La mémoire personnelle reste dans Settings. Le résumé et l'ancien résumé
  d'archive passent dans le panneau **Summary** du chat, via
  `/api/chat/context?session=...`. Les corrections conservent leurs contrôles de
  concurrence, de droits et de frontière de compaction. Un changement de chat
  ferme le panneau ; les brouillons restent attachés à leur conversation.
- Les documents et leur suivi d'indexation utilisent l'identité et le groupe,
  sans session de workspace. La maintenance globale des ressources n'utilise
  plus le dernier workspace du navigateur ; les liens de ressources et les
  compléments de personnalité continuent à viser un workspace choisi explicitement.
- Tous les accès MCP (interface, agent, catalogue initial, prompt, résolution et
  notifications) consultent le registre de l'installation en mono ou du groupe
  en multi. Le choix du workspace n'intervient plus. Les anciennes connexions
  mono sont reprises en transaction, avec leurs secrets, sans écraser un ID
  existant ni modifier l'état activé/désactivé. Les registres de groupe sont exclus.
- Une ancienne connexion `Image` et `image-gen` exposaient tous deux
  `generate_image` dans l'instance locale. Les connexions déjà globales gardent
  la priorité lors de cette reprise. Les erreurs de lecture MCP sont affichées
  comme des erreurs, plutôt que comme une absence de configuration.

La relecture a aussi vérifié les scopes de compétences, les préférences d'outils,
les modèles, les connexions personnelles, les choix de groupe de l'administration
et les travaux affichés dans Activité. Leur cible est l'identité ou le groupe
explicite ; les données de conversation et de ressources gardent une cible
explicite lorsqu'elle est nécessaire. Aucun nouveau paramètre d'outil n'est ajouté.

Validation : tests Go avec PostgreSQL jetable et détecteur de courses, `go vet`,
tests Node et parcours Chromium de séparation réglages/chat, correction refusée
puis réussie, navigation mobile, MCP, maintenance des ressources et indexation.
La migration est vérifiée avec doublon d'ID, collision de nom d'outil, secret
scopé, état OAuth, serveur désactivé, seconde exécution et exclusion des groupes.

Livraison après la seconde revue : `prism-server` et `prism-mu-server` ont été
recréés avec `sha256:0b09d962c137ac926a6f1d9c078365555957c0467b5b5cb764138504361aaf7a`
(`blazux/prism:local-settings-fix-20261110`). Le modèle, les bases et les conteneurs
de travail n'ont pas été redémarrés. Une sauvegarde privée des configurations MCP
et secrets chiffrés précède la migration.

Vérification réelle : l'interface locale affiche `image-gen` et ses six outils,
ainsi que l'ancienne connexion `Image`. Les listes API et `mcp action=list` sont
identiques depuis `default`, `scan` et `assistant`. L'endpoint de mémoire
personnelle ne renvoie plus de résumé ni de session ; le résumé reste lisible par
l'endpoint du chat. Les deux services sont en fonctionnement. Aucun commit ni
push n'a été effectué.


## Troisième revue — contrats d'exécution et retours de l'interface

Chantier du 10 novembre 2026, fondé sur la revue de compréhension de Prism.
Les modifications de cette passe sont préparées depuis une copie du checkout
courant afin de préserver les changements locaux précédents.

- Les préférences d'outils ciblent les outils composites réellement exposés.
  Les anciens opt-outs maintiennent la capacité correspondante désactivée et
  une notice explique comment la réactiver. Les noms personnalisés sont préservés.
- Une sauvegarde acquitte les contrôles explicitement envoyés. Les tests de
  connexion ne valent pas sauvegarde ; une erreur conserve les saisies. La
  fenêtre workspace attend les réponses HTTP et protège lecture retardée et
  double clic. Les filtres d'Activité restent accessibles sur petit écran.
- Une réponse modèle interrompue conserve son texte partiel, sans exécuter son
  lot d'outils incomplet. Les commandes remontent leur erreur avec leurs sorties.
  Le garde de répétition avertit avant l'arrêt d'un cycle persistant et laisse
  le modèle conclure ou changer d'approche ; ce n'est pas une mesure sémantique
  de progression. Le budget global reste inchangé.
- Les outils personnalisés utilisent un parseur d'en-tête commun et une
  validation avant écriture. L'enregistrement invalide préserve l'ancien outil.
  Les modifications de métadonnées par shell/terminal sont détectées via
  mtime/taille. L'endpoint HTTP historique partage le contexte d'exécution et
  les secrets autorisés, tout en gardant son format de réponse. Les appels
  déterministes ne requièrent pas de configuration modèle.
- Les traces au point de résolution identifient tâche, requête, source,
  fournisseur, destination configurée nettoyée, statut HTTP observé et fin du
  flux. Elles ne contiennent ni prompt ni secret et ne supposent pas connaître
  le serveur derrière une passerelle.
- Les demandes d'inventaire/nettoyage peuvent quitter l'attente du verrou si
  elles sont annulées. Chaque suppression conserve un plan recalculé sous
  verrou. Les mesures synthétiques ne justifient pas un cache d'affichage.
  Le nouveau suivi des outils rendait leur inventaire quadratique en lectures
  de métadonnées ; réutiliser la liste puis la revalider une fois évite ce coût.

Validation technique de cette passe : `go test -race ./...` avec PostgreSQL
jetable et pgvector dans le schéma public, `go vet ./...`, 25 suites Node et
syntaxe des scripts réussis. Un test supplémentaire PostgreSQL vérifie la
reconstruction d'un agent après interruption : texte partiel conservé, aucun
appel d'outil incomplet archivé ou rejoué. Les vérifications navigateur avec
API synthétique couvrent les champs de même valeur sauvegardés séparément,
HTTP 500 gardant la fenêtre et la saisie, migration des opt-outs et Activité
à 360/390 px sans débordement global, dernier onglet et Refresh accessibles.

Mesures synthétiques, sans appel d'inférence : l'inventaire prend environ
0,48 ms pour 100 fichiers et 5,32 ms pour 1 000. Avec le nouveau suivi des
métadonnées mais l'ancien inventaire répétant `Get`, 100/500 outils prenaient
57,5/1 185 ms ; réutiliser `All` et revalider une fois donne 3,71/29,8 ms sur les
mêmes jeux (trois répétitions). Ce sont des mesures locales limitées, pas une
promesse de latence générale ni un gain sur la production précédente.

### Comparaison réelle de cette passe

Deux campagnes appariées de cinq scénarios existants répétés trois fois, soit
30 essais par version et 60 au total. Même modèle `flash-next`, configuration
et contrôles ; instances, workspaces et schémas PostgreSQL jetables. Instructions
et catalogue initiaux identiques. Aucun correcteur ni seuil modifié pour faire
passer la comparaison.

| Mesure brute cumulée | Avant | Après |
| --- | ---: | ---: |
| Scénarios acceptés par les contrôles | 29/30 | 28/30 |
| Appels d'outils totaux | 31 | 27 |
| Erreurs d'exécution d'outils | 0 | 0 |
| Durée moyenne brute | 8,473 s | 6,659 s |

La **gate brute n'est pas validée**, à cause du taux de réussite. Inspection des
trois réponses rejetées : une vraie erreur horaire avant (21:00 au lieu de
22:00), une vraie erreur de calcul après (F20 = 4181 au lieu de 6765), et une
réponse correcte après (`10 p.m.`) rejetée parce que l'expression régulière
n'accepte pas cette ponctuation. Cette dernière est un faux négatif identifié ;
les rapports bruts restent inchangés. En tenant compte de cette seule lecture,
29/30 réponses sont correctes de chaque côté. Cela ne démontre ni un gain de
qualité, ni l'absence générale de régression. La durée et les appels varient
avec les choix du modèle et les caches ; ne pas en déduire un gain garanti.

Rapports : `/tmp/prism-contracts-model-eval-20261110/combined-summary-60.json`,
`raw-combined-baseline.json`, `raw-combined-candidate.json` et les quatre
rapports originaux dans le même dossier. Les fichiers du chantier sont intégrés
au checkout local après comparaison avec sa copie initiale ; les changements
préexistants sont conservés. Aucun commit, push ou déploiement de production.
Les conteneurs et schémas de la comparaison ont été nettoyés.


### Installation locale pour essais utilisateur — mono et multi

À la demande explicite de l'utilisateur, les deux serveurs locaux ont été
recréés l'un après l'autre avec l'image `blazux/prism:local-contracts-20261110-222727`,
ID `sha256:f0099d0a38acd043a840ad5412c8808a37d274d698002e9a06c6ba73d808a095`. L'alias Docker local `blazux/prism:latest`
réfère à cette image pour les commandes Compose habituelles. Aucune image publiée.

- Mono : `prism-server`, http://localhost:48080, 13 espaces conservés.
- Multi : `prism-mu-server`, http://localhost:48081, 29 espaces et 2 Rooms conservés.
- Les contrôles d'activité n'ont détecté aucun travail en cours sur les espaces
  et Rooms accessibles ; les journaux ne montraient aucun tour de canal engagé
  sans fin correspondante, et aucun Docker exec n'était actif dans les workspaces.
  Les anciennes API ne fournissent pas de compteur global exhaustif des jobs
  headless ; ne pas élargir cette observation en garantie générale.
- Les paramètres d'environnement, modèles et embeddings configurés, ports,
  montages et réseaux correspondent à ceux d'avant la recréation. Les API de
  statut, configuration et sessions répondent en HTTP 200 ; les fichiers UI
  servis correspondent exactement au source. En multi, les contrôles des
  ressources protégées ont utilisé l'authentification existante.
- Aucun redémarrage des bases, conteneurs de travail ou modèles. Aucun message
  envoyé ni conversation de test ajoutée aux instances utilisateur.
- Les deux conteneurs sont en fonctionnement, compteur de redémarrages à zéro,
  sans erreur sévère de démarrage détectée. L'installation locale permet les
  essais utilisateur ; elle ne change pas les limites du bilan de comparaison
  réelle ci-dessus.

Images précédentes conservées :
`blazux/prism:local-before-contracts-mono-20261110-222727` et
`blazux/prism:local-before-contracts-multi-20261110-222727`
(ID commun `sha256:0b09d962c137ac926a6f1d9c078365555957c0467b5b5cb764138504361aaf7a`).
Rendus de configuration privés et vérification de déploiement sous `/tmp/prism-local-upgrade-*`.
Aucun commit ni push.


## Préparation V1 terminée — 10 novembre 2026

- Candidat final main : `9a1c6928ab1b572d52df9f207fd4eba01440573a`, commité et poussé avec l'identité Git Blazux. Notes publiques dans `docs/releases/v1.0.0.md` ; ce compte rendu reste local et non commité.
- CI complète verte : https://github.com/blazux/prism/actions/runs/42948933419 . Go vet/race sur PostgreSQL neuf avec pgvector/public, 26 suites Node, quatre scripts navigateur et scan des appels Go validés. Deux attentes asynchrones du test navigateur ont été corrigées sans retirer d'assertion.
- Cinq parcours isolés validés : installations neuves mono/multi, mises à jour v0.8.2 mono/multi, sauvegarde/restauration PostgreSQL + workspace + même clé privée vers d'autres volumes. Données et secrets chiffrés conservés. Retour utilisateur : aucun blocage observé sur ses deux instances.
- Images publiques `unstable` serveur/workspace publiées AMD64 et ARM64 ; manifests vérifiés. Un smoke natif AMD64 supplémentaire sur l'image serveur publique immuable valide authentification/API/persistance/restart. Pas d'exécution ARM locale. Tags publics latest inchangés.
- Tous les conteneurs, volumes et réseaux de validation ont été nettoyés ; aucun appel modèle réel pendant ces essais isolés. Les instances utilisateur restent sur leur image validée f0099d0a… sans nouveau redémarrage.
- Preuves agrégées : `/tmp/prism-v1-release-validation.json`, `/tmp/prism-v1-validation-checklist.md`, `/tmp/prism-v1-ci-final-9a1c692.json`, `/tmp/prism-v1-install-upgrade-smoke/aggregate.json`, `/tmp/prism-v1-public-final-smoke/aggregate.json`.
- Publication stable : aucun tag `v1.0.0` ni release GitHub créé durant cette préparation. Les notes sont encore marquées draft.


## Publication stable v1.0.0 terminée

- Tag final annoté `v1.0.0` : objet `ac3e5337a66f5c21a2a9ae2d206dde155b471e96`, commit `e866ebe5d8cf9f9871b74ecbb78f101ce75b06e5`, main et tag poussés avec l'identité Blazux.
- Le premier tag sur 2c55813 a été bloqué par la nouvelle vulnérabilité Go HTTP GO-2026-6859/CVE-2026-97054. Aucune image stable ni release n'avait été publiée. Correctif officiel Go 1.26.11 intégré après reproduction ciblée et full tests Go/race/vet sur base neuve. Le tag a été corrigé avec lease exacte et push atomique, sans supprimer le contrôle de sécurité.
- CI du tag entièrement verte : https://github.com/blazux/prism/actions/runs/42954072435 . Publication après build par un job GitHub dédié, permission contents:write limitée à ce job.
- Release stable publique et latest : https://github.com/blazux/prism/releases/tag/v1.0.0 . Notes comparées exactement à leur source dans le tag ; aucune mention Draft.
- Les images serveur et workspace v1.0.0/latest ont chacune les mêmes digests et les plateformes AMD64/ARM64. Test natif amd64 supplémentaire du couple exact : auth/API/secret chiffré/custom Python/fichiers/archive/restart validés ; Go 1.26.11 lu dans le binaire réellement téléchargé. Aucun modèle réel, aucun changement aux instances de production, nettoyage complet.
- Preuves : `/tmp/prism-v1-stable-publication.json`, `/tmp/prism-v1-release-final-e866.json`, `/tmp/prism-v1-tag-e866-hub.json`, `/tmp/prism-v1-stable-pair-smoke/aggregate.json`, `/tmp/prism-v1-go11-checks/aggregate.json`.
- Suite demandée par l'utilisateur : même genre de revue produit/philosophie/interface/backend/architecture pour PrismVox, après cette publication.

## Optimisations transparentes — 18 novembre 2026

Après les correctifs de la revue, publiés dans `2718750`, cette passe conserve
les outils, leurs schémas, les instructions et les contenus envoyés au modèle.
Les nouvelles fonctionnalités restent à évaluer séparément.

- Documents utilise une seule implémentation JS/CSS dans Settings et sur
  `/rag.html`. Chaque instance garde ses propres requêtes et contrôles de
  concurrence ; quitter l'onglet détruit ses abonnements et minuteries.
- CodeMirror, ses dix modes et ses styles sont chargés au premier fichier.
  Un éditeur texte permet de travailler et sauvegarder pendant le chargement
  ou son échec. Réouverture, changement de fichier et reprise du chargement
  préservent les modifications. Le démarrage évite douze requêtes nettes et
  573 791 octets bruts ; ce n'est pas une mesure de latence réseau réelle.
- Les lectures indépendantes d'identité, ainsi que sessions/téléphonie,
  démarrent en parallèle. Un échec isolé ne supprime pas les autres résultats ;
  les protections contre les réponses de sessions périmées restent actives.
- Le cache du moteur porte uniquement sur le préfixe statique des instructions.
  Sa clé inclut nom, session, personnalité et profil. Les droits, catalogues,
  connexions, connaissances et vues restent relus à chaque appel. Le JSON des
  outils déjà sérialisé est réutilisé pour les mesures de consommation.
- Chat, recherche et compaction partagent la collecte des événements modèle,
  en conservant leurs politiques distinctes pour texte partiel, erreur et fin
  de génération. Images, raisonnement, outils et blocs opaques restent conservés.
- L'ancien `MaybeSummarize`, sans appel, et son helper privé sont supprimés.
  La lecture/correction des résumés historiques et la compaction active restent
  compatibles. Aucun changement de stockage des résultats longs.

Validation : suite Go avec détecteur de courses et PostgreSQL jetable, `go vet`,
34 suites Node et dix parcours Chromium isolés réussis. Les parcours Documents,
éditeur et Markdown rejoignent la CI. Les requêtes Go et JSON comparées aux
fonctions originales sont strictement identiques sur douze cas : trois profils,
chat/voix, rappel présent/absent, avec outils et images, cache froid puis chaud.

Microbenchmark du seul assemblage du prompt guidé : moyenne de 35,3 à 3,34 µs,
197,6 à 33 ko alloués et 31 à 5 allocations. Ce résultat mesure du travail CPU
interne ; il ne démontre aucun gain de génération, de préremplissage ou de qualité
du modèle. Les données live ne sont pas mises en cache faute d'invalidation
fiable des changements externes.

### Installation locale et démos

Les deux serveurs locaux et les deux démos du VPS ont été recréés avec la même
image, `sha256:28fa8d21ec335c4b5d7526ea63324c7fd588ea25cd0a39718384d3429d9fc330`.
Leurs configurations, volumes, conversations et connexions ont été conservés.
Les contrôles d'activité précédaient chaque remplacement ; les autres services
ont gardé leurs conteneurs et leurs dates de démarrage.

Après installation : API authentifiées, refus des accès anonymes et neuf fichiers
web comparés au checkout sur les quatre instances. Le site et les deux démos
répondent également en HTTPS. Les images précédentes et les sauvegardes des
bases sont conservées pour permettre un retour arrière. Aucun appel de génération
n'a été nécessaire pour ces contrôles de déploiement.

## Capacités retenues après les optimisations — 18 novembre 2026

L'utilisateur a retenu les propositions 1, 3 et 4. Le premier chantier est
réduit à la reprise explicite après interruption : fermer un onglet ou changer
de workspace n'interrompt déjà pas le travail. Aucun moteur de workflow, nouvel
outil modèle ou journal de checkpoints n'est ajouté.

- Reprise : Activity liste les conversations personnelles interrompues et ouvre
  leur chat. Resume envoie un tour ordinaire sans écraser le brouillon séparé.
  Les appels d'outils sans résultat sauvegardé reçoivent un résultat synthétique
  « état inconnu » dans le contexte, sans modifier l'archive ni réexécuter
  l'action. La frontière de compaction conserve ensemble les lots d'outils et
  leurs notes intercalées, y compris quand leurs IDs ne sont plus croissants.
- Références : des URL ordinaires ouvrent un email, une note, une tâche, un
  événement ou une conversation existante. Les applications résolvent l'objet
  dans les sources et droits du compte courant ; le lien ne transporte ni
  identité ni secret. Les liens d'applications passent par leur protocole
  d'éditeur pour préserver les modifications non enregistrées.
- Réactivité : des signaux d'invalidation nommés complètent `prismOnData`.
  Le dashboard réutilise son WebSocket ; scripts et prévisualisations autonomes
  peuvent écouter en SSE. Les mutations natives publient après succès ; les
  outils personnalisés publient explicitement après sauvegarde. Le consommateur
  relit sa source autorisée, sans tour de modèle imposé. Les signaux ne portent
  aucun enregistrement ; les droits personnels/de groupe sont revérifiés.

Limites : reprise volontaire à partir de l'historique, pas d'exécution exactement
une fois ni de restauration des processus. Les pixels d'images ne sont pas
réhydratés au rechargement, même si leurs chemins restent dans la conversation.
Les événements ne sont pas une archive durable : connexion et reconnexion
invalident l'état local et demandent une nouvelle lecture. Un fichier partagé
conserve ses propres droits ; un signal de groupe ne les remplace pas.

Validation de cette passe : `go test -race ./...` sur PostgreSQL/pgvector
jetable, `go vet ./...`, 37 suites JavaScript et les 13 parcours Chromium de
la CI réussis. Les nouveaux parcours couvrent mono/multi, reprise volontaire
et brouillon séparé, liens chat/widget `srcdoc`, sauvegarde échouée ou frappe
pendant sauvegarde, annulation de sortie, huit widgets sur un seul WebSocket
et rafraîchissement après reconnexion. Les tests Go couvrent aussi compaction
et rechargement des lots incomplets, adaptateurs de modèle stricts, droits
des objets, occurrences CalDAV et révocation des notifications. Ils ne
mesurent pas la qualité de décision d'un modèle après reprise.

Le parcours de partage des notes a révélé une requête `ShareNote` avec sept
placeholders pour six valeurs : correction incluse et exercée sur la base
jetable. Les deux autres régressions corrigées pendant la revue concernent
le rafraîchissement des tâches après `cron` et l'absence de callback pour les
canaux mono Slack/webhooks sans propriétaire. Aucun commit, redémarrage ni
changement aux instances réelles pendant cette passe.

### Installation locale et démos des trois capacités

À la demande de l'utilisateur, les serveurs mono/multi locaux et les deux
démos ont été recréés avec l'image commune
`sha256:a1134ed76ccb257994976fee3f1fed2a59e221a34fec218b10cf46fb0810a465`.
Les configurations, montages, ports, réseaux et limites correspondent aux
conteneurs précédents. Les démos conservent leur image épinglée dans leurs
fichiers d'environnement ; les alias d'image locaux sont actualisés.

Les contrôles d'activité précédaient chaque remplacement. Les quatre bases et
les paramètres précédents sont sauvegardés, et les anciennes images gardées.
Après remplacement, 17 fichiers web correspondent exactement au checkout sur
chaque instance ; le flux de notifications authentifié, l'aide embarquée et le
refus des API privées anonymes sont vérifiés. Les trois sites HTTPS répondent,
sans erreur fatale de démarrage ni redémarrage automatique des serveurs observé.
Les conteneurs annexes, dont le site, les workspaces, bases et modèles, ont
conservé leurs identifiants et dates de démarrage. Aucun message de test envoyé
aux agents. Aucun commit ni push pendant ce déploiement.

Preuves et sauvegardes privées locales :
`/tmp/prism-features-deploy-20261119-024134`. Sauvegardes et vérification des
démos sur le VPS : `/root/prism-features-deploy-20261119-024134`.

### Confort du chat et nouvelle installation

Le chat, la Room et la supervision téléphonique affichent 163 raccourcis sous
forme de 76 emoji, sans modifier les messages envoyés ou conservés. Les zones
de code et les liens restent littéraux ; les infobulles du sélecteur indiquent
les noms disponibles. Neuf thèmes sombres utilisent un chat translucide avec
flou ; les préférences de transparence réduite ou de contraste renforcé gardent
un fond opaque. Contrastes vérifiés en navigateur sur les 22 thèmes, mobile et
préférences d'accessibilité compris. Les 38 suites JavaScript et les 13 parcours
Chromium passent. La sauvegarde lente d'une note a permis de reproduire puis
corriger une course d'ouverture de lien ; le test force désormais ce délai et
utilise une attente bornée.

Les quatre serveurs ont ensuite été recréés à la demande de l'utilisateur avec
`sha256:6f01741b1b3f13662e262a2f4d6c95a9fe1a8f4f7ef37464367d6934a1bccd3d`.
Les contrôles vérifient 22 fichiers web et l'aide embarquée, les accès privés,
les notifications et les démos HTTPS. Paramètres, données et services annexes
conservés ; anciennes images et sauvegardes gardées. Preuves privées locales :
`/tmp/prism-comfort-deploy-20261119-033129`, et sur le VPS :
`/root/prism-comfort-deploy-20261119-033129`. Aucun commit ni push Git.

### Correction de la visibilité du fond de chat

Retour utilisateur sur Cyberpunk : la feuille de style était bien déployée,
mais le voile à 80 % et le flou de 14 px effaçaient visuellement le circuit.
Les contrôles initiaux d'alpha et de contraste ne vérifiaient pas suffisamment
la perception du décor. Le panneau laisse maintenant passer le fond avec une
teinte de 25 % et un flou de 2 px ; les cartes complètes des messages portent
une surface à 92 %, et les barres restent opaques. L'état glass/solid est défini
par le thème et se réinitialise aussi lors des prévisualisations personnalisées.

Validation : 22 thèmes et 440 mesures de contraste composé (minimum 5,27:1),
préférences d'accessibilité, mobile, comparaison pixel sur un même circuit figé,
38 suites JavaScript et cinq parcours navigateur concernés. Preuves visuelles :
`/tmp/prism-chat-glass-v2-*`. Les quatre serveurs ont été recréés avec
`sha256:35da893232f7835eb582b2f1aee0bb876b494642b587a6eb2df9df248a64e0bf`.
Les fichiers servis sur les domaines HTTPS correspondent au checkout. Comptes,
conversations, configurations et services annexes conservés. Sauvegardes et
contrôles : `/tmp/prism-glass-deploy-20261119-034650` en local et
`/root/prism-glass-deploy-20261119-034650` sur le VPS. Aucun commit ni push Git.

### Circuit Cyberpunk plus actif

Le fond Cyberpunk comporte davantage de composants, de pistes et de signaux,
avec des impulsions circulant dans les deux sens à 220–360 px/s. À 1440 × 1000,
le circuit passe de 120 à 280 pistes et de 16 à 55 impulsions ; la vitesse
moyenne mesurée sur le même échantillon passe de 84 à 292 px/s. Le décor fixe
reste mis en cache. Les autres animations ne changent pas.

Validation : 38 suites JavaScript, parcours navigateur des thèmes, géométrie
mobile et 4K, vitesse équivalente à 60/120/144 Hz, pause des onglets masqués,
intensité zéro et réduction du mouvement. Les quatre serveurs utilisent l'image
`sha256:541722c84a030b6a4b530ad8c4f1fab14c5c292438596fece07f0db82bcd5c74`.
Les 23 fichiers web, dont le script d'animation, correspondent au checkout ;
les domaines HTTPS des démos servent la nouvelle version. Configurations et
comptes conservés, bases sauvegardées, anciennes images gardées. Contrôles et
sauvegardes privés : `/tmp/prism-circuit-deploy-20261119-035941` en local et
`/root/prism-circuit-deploy-20261119-035941` sur le VPS. Aucun commit ni push Git.
