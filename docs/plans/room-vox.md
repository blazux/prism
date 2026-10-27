# Room, supervision PrismVox et mode personnel

Plan de travail interne — 26 octobre 2026.

## Principes retenus

Room est le lieu de travail collectif d'un groupe, même sans téléphonie. Son
archive reste consultable ; le contexte envoyé au modèle reste borné. Les appels
d'outils font partie de la conversation visible et persistent après rechargement.

La supervision Vox consulte les archives à la demande. Aucun choix de période
n'est imposé avant de poser une question. Une primitive de lecture paginée suffit :
l'agent choisit les filtres et compose ses analyses. Les journaux gardent leurs
filtres pour la consultation manuelle.

Le raccordement fonctionne en personnel comme en multi. Le numéro du profil du
propriétaire personnel identifie son assistant ; les clients rencontrent le
standard. Les données personnelles ne deviennent pas accessibles au standard.

## Jalons et critères de validation

### 1. Room utilisable dans la durée

- Sérialiser les demandes d'un groupe ; permettre l'arrêt de la réponse.
- Diffuser le texte et les appels d'outils ; conserver les traces dans l'archive.
- Charger les anciens messages par pages.
- Borner les échanges rejoués et les messages ambiants injectés au modèle.
- Afficher le contexte conservé et permettre son allègement sans effacer l'archive.
- Vérifier les droits entre groupes, la concurrence et le rechargement.

### 2. Supervision autonome de Vox

- Remplacer l'instantané des quinze appels par une lecture d'archive à la demande.
- Lire les métadonnées puis les détails seulement si nécessaire, avec pagination.
- Filtrer par période, numéro, direction et texte ; retourner le total correspondant.
- Persister la conversation de supervision, ses outils et un contexte borné.
- Vérifier les archives anciennes, les limites et la reprise après redémarrage.

### 3. Espace PrismVox et mode personnel

- Présenter le travail quotidien dans un espace PrismVox ; garder les réglages
  techniques dans les paramètres.
- Autoriser les groupes existants, sans seconde liste de membres.
- Propager une audience fiable pour les nouveaux appels : standard, personnel,
  ou non classé. Les anciens appels non classés restent réservés à l'administrateur.
- Appliquer les droits aux listes, détails, totaux et lectures de l'agent.
- Raccorder le profil personnel à l'assistant téléphonique et à ses outils usuels.
- Vérifier personnel, membre autorisé, autre groupe et appelant extérieur.

### 4. Livraison locale et documentation

- Migrations additives ; aucune suppression des conversations existantes.
- Tests ciblés des parcours, des scopes et des erreurs de fournisseur.
- Documentation fonctionnelle en français et en anglais, avec les limites réelles.
- Rebuild des services locaux nécessaires, sans redémarrer le modèle GPU arrêté.
- Conserver le raccordement de test actuel tant que le nouveau parcours n'est pas
  validé. Commit, push et publication feront l'objet d'une demande distincte.

## Suivi

- [x] État initial des deux dépôts et des chemins d'identité inspecté.
- [x] Jalon 1 validé.
- [x] Jalon 2 validé.
- [x] Jalon 3 validé.
- [x] Jalon 4 validé localement ; essai téléphonique réel à faire avec le modèle relancé.

## Points de vigilance concrets

Le propriétaire personnel utilise l'identité 0 et le scope global, sans ligne
dans `users`. Le raccorder artificiellement à `u0` donnerait un agenda et des
mails vides. Un groupe ne doit pas accéder au proxy d'administration Vox brut.
Une conversation privée reconnue ne doit pas être visible dans l'archive du
standard. Le service téléphonique authentifie la passerelle, pas l'interlocuteur.

## Validation effectuée

- Les paquets Go `internal/server` et `internal/agent` de Prism passent avec une
  base pgvector jetable. Les cas ajoutés couvrent file Room, traces persistées,
  scopes, préférences de transfert du propriétaire et résumé concurrent.
- Les paquets Go `call`, `llm` et `db` de Vox passent.
- Les tests Python couvrent 37 appels paginés, une recherche au-delà des quinze
  derniers, périodes/numéros/directions, détails volumineux, appels privés,
  propriétaire 0, deux installations, reprise de supervision et erreurs de résumé.
- La suite Python termine avec 16 tests exécutés et un test TLS ignoré faute
  de son prérequis dans le conteneur de test.
- Les tests Node vérifient l'affichage des résultats, échecs et sorties contenant
  du HTML. Les interfaces ont été parcourues dans le navigateur sur des données
  fictives, avec réponse Room, outils, rechargement et panneau de contexte.
- Le site bilingue se construit et ses contrôles de pages, liens et références
  d'environnement passent. La nouvelle documentation est publiée localement.
- Le raccordement multi réel retourne les 14 anciens appels dans la vue privée
  administrateur. Le standard ne reçoit pas leurs données non classées.

## État local demandé

PrismVox est reconnecté au Prism multi. Le Prism personnel contient les mêmes
modifications, mais reste sans connexion Vox. L'accès des groupes se configure
avec le bouton Accès du workspace ; aucun groupe n'a été autorisé automatiquement.
Le modèle Qwen 27B reste arrêté à la demande précédente de l'utilisateur. Les
échanges de modèle des tests utilisent un faux fournisseur ; aucun appel réel
n'a été déclenché. Les changements ne sont ni committés ni poussés, et le VPS
n'a pas été modifié pendant ce chantier.


### Correctif du client Room intégré

Le test utilisateur a révélé que le shell ouvrait `web/apps/room.html`, tandis
que la première validation visuelle portait sur l'ancien point d'entrée `/room`.
Le client intégré ajoutait chaque événement `message` comme une nouvelle ligne,
y compris les mises à jour de la réponse commencée. Il affiche désormais une
seule ligne par identifiant et traite les événements de streaming, outils et
activité. Les commandes d'archive et de contexte sont raccordées dans ce client.
`/room` redirige vers cette même page ; l'interface dupliquée a été supprimée.
Les citations automatiques restent réservées aux réponses séparées de leur
question par d'autres messages. Tests Node sur les vrais gestionnaires de frames
et vérification navigateur du point d'entrée du shell, y compris rechargement.


### Découverte de l'archive dans la Room

Le test du 27 octobre a montré que le modèle tentait `list_calls`, outil de file
sortante interdit au groupe, au lieu du lecteur d'archives. La recette n'était
visible que dans le résultat de `prism_help`, et aucun groupe local n'avait reçu
l'accès Vox. Les agents partagés reçoivent maintenant les faits d'accès courants
et la recette HTTP dans leur contexte système, sans appel d'aide préalable. Le
catalogue masque les outils interdits par leur politique de groupe, et l'API
refuse explicitement un groupe non autorisé au lieu de renvoyer un faux historique
vide. Les droits d'appels sortants ne sont pas élargis. Tests sur le prompt réel
avec fournisseur fictif, les capacités signées et l'isolation entre groupes.

Validation locale du correctif : le jeton signé `room-g2` reçoit un refus HTTP 403
explicite tant que le groupe n'est pas autorisé. Le même appel via la primitive
réelle `exec_command`, depuis le workspace, atteint correctement ce refus :
URL interne, authentification et portée ont donc été vérifiées bout en bout.
L'utilisateur a ensuite explicitement autorisé `betatest`. L'accès de ce groupe
a été activé via l'API de configuration en conservant les autres autorisations.
La lecture signée de la Room, le même appel via `exec_command` depuis le
workspace et la lecture du workspace PrismVox réussissent. Leur périmètre
retourne actuellement zéro appel accessible ; les anciennes archives non classées
et les appels personnels restent exclus du groupe.
